package secureio_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xaionaro-go/slice"
	"github.com/xaionaro-go/unsafetools"

	xerrors "github.com/xaionaro-go/errors"

	. "github.com/xaionaro-go/secureio"
)

func TestSessionBigWrite(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{
		EnableDebug: true,
	}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	writeBuf := make([]byte, sess0.GetPayloadSizeLimit())
	rand.Read(writeBuf)
	readBuf := make([]byte, sess0.GetPayloadSizeLimit())

	_, err = sess0.Write(writeBuf)
	assert.NoError(t, err)

	_, err = sess1.Read(readBuf)
	assert.NoError(t, err)

	assert.Equal(t, writeBuf, readBuf)

	assert.NoError(t, sess0.Close())
	assert.NoError(t, sess1.Close())

	waitForClosure(t, sess0, sess1)
}

func waitForClosure(t *testing.T, sesss ...*Session) {
	for _, sess := range sesss {
		sess.WaitForClosure()
		assert.Equal(t, SessionStateClosed, sess.GetState())
	}
}

func TestSessionWaitForSendInfo(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{
		EnableDebug: true,
	}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	writeBuf := make([]byte, 8)
	rand.Read(writeBuf)
	readBuf := make([]byte, 8)

	sendInfo := sess0.WriteMessageAsync(MessageTypeReadWrite, writeBuf)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		_, err := sess1.Read(readBuf)
		assert.NoError(t, err)

		wg.Done()
	}()

	<-sendInfo.Done()
	assert.NoError(t, sendInfo.Err)
	sendInfo.Release()

	wg.Wait()
	assert.Equal(t, writeBuf, readBuf)

	assert.NoError(t, sess0.Close())
	assert.NoError(t, sess1.Close())

	waitForClosure(t, sess0, sess1)
}

func TestSessionAsyncWrite(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	sendLogger := &testLogger{t}
	opts := &SessionOptions{
		EnableDebug: true,
		//SendDelay: &[]time.Duration{0}[0],
	}

	sess0 := identity0.NewSession(identity1, conn0, sendLogger, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	var wg sync.WaitGroup

	writeBuf := make([]byte, sess0.GetPayloadSizeLimit()/4)
	rand.Read(writeBuf)

	for i := 0; i < 10; i++ {
		func() {
			readBuf := make([]byte, sess0.GetPayloadSizeLimit()/4)

			wg.Add(1)
			go func() {
				sendInfo := sess0.WriteMessageAsync(MessageTypeReadWrite, writeBuf)
				<-sendInfo.Done()
				assert.NoError(t, sendInfo.Err)
				sendInfo.Release()
				wg.Done()
			}()

			wg.Add(1)
			go func() {
				_, err := sess1.Read(readBuf)
				assert.NoError(t, err)

				assert.Equal(t, writeBuf, readBuf)
				wg.Done()
			}()
		}()
	}

	wg.Wait()

	assert.NoError(t, sess0.Close())
	assert.NoError(t, sess1.Close())

	waitForClosure(t, sess0, sess1)
}

func TestSession_WriteMessageAsync_noHanging(t *testing.T) {
	benchmarkSessionWriteRead(
		&testing.B{N: 10000},
		1, 0, true, false, false, false,
		&testLogger{t},
	)
}

func BenchmarkSessionWriteRead1(b *testing.B) {
	benchmarkSessionWriteRead(b, 1, 0, false, true, false, false, nil)
}
func BenchmarkSessionWriteRead16(b *testing.B) {
	benchmarkSessionWriteRead(b, 16, 0, false, true, false, false, nil)
}
func BenchmarkSessionWriteRead1024(b *testing.B) {
	benchmarkSessionWriteRead(b, 1024, 0, false, true, false, false, nil)
}
func BenchmarkSessionWriteRead32000(b *testing.B) {
	benchmarkSessionWriteRead(b, 32000, 0, false, true, false, false, nil)
}
func BenchmarkSessionWriteRead64000(b *testing.B) {
	benchmarkSessionWriteRead(b, 64000, 0, false, true, false, false, nil)
}
func BenchmarkSessionWriteMessageAsyncRead1(b *testing.B) {
	benchmarkSessionWriteRead(b, 1, 0, true, false, false, false, nil)
}
func BenchmarkSessionWriteMessageAsyncRead16(b *testing.B) {
	benchmarkSessionWriteRead(b, 16, 0, true, false, false, false, nil)
}
func BenchmarkSessionWriteMessageAsyncRead1024(b *testing.B) {
	benchmarkSessionWriteRead(b, 1024, 0, true, false, false, false, nil)
}
func BenchmarkSessionWriteMessageAsyncRead32000(b *testing.B) {
	benchmarkSessionWriteRead(b, 32000, 0, true, false, false, false, nil)
}
func BenchmarkSessionWriteMessageAsyncRead64000(b *testing.B) {
	benchmarkSessionWriteRead(b, 64000, 0, true, false, false, false, nil)
}

func BenchmarkSessionWriteMessageAsyncRead1300_max1400(b *testing.B) {
	benchmarkSessionWriteRead(b, 1300, 1400, true, false, false, false, nil)
}
func BenchmarkSessionWriteMessageAsyncRead60000_max1400_withFrag(b *testing.B) {
	benchmarkSessionWriteRead(b, 1300, 1400, true, false, true, false, nil)
}

func BenchmarkSessionWriteMessageAsyncRead1300_max1400_udp(b *testing.B) {
	benchmarkSessionWriteRead(b, 1300, 1400, true, false, false, true, nil)
}
func BenchmarkSessionWriteMessageAsyncRead60000_max1400_udp_withFrag(b *testing.B) {
	benchmarkSessionWriteRead(b, 1300, 1400, true, false, true, true, nil)
}

func benchmarkSessionWriteRead(
	b *testing.B,
	blockSize uint,
	maxPayloadSize uint,
	shouldWriteAsMessage bool,
	isSync bool,
	enableFragmentation bool,
	isUDP bool,
	eventHandler EventHandler,
) {
	if !isSync && !shouldWriteAsMessage {
		panic(`!isSync && !shouldWriteAsMessage`)
	}

	b.ReportAllocs()

	ctx, cancelFunc := context.WithCancel(context.Background())

	var identity0, identity1 *Identity
	var conn0, conn1 net.Conn
	if isUDP {
		identity0, identity1, _, _ = testPair(nil)
		conn0, conn1 = testUDPPair(nil)
	} else {
		identity0, identity1, conn0, conn1 = testPair(nil)
	}

	if eventHandler == nil {
		eventHandler = wrapErrorHandler(&dummyEventHandler{}, func(sess *Session, err error) bool {
			if errors.Is(err, io.EOF) || errors.As(err, &ErrAlreadyClosed{}) {
				return false
			}
			if pathErr := (*os.PathError)(nil); errors.As(err, &pathErr) {
				panic(fmt.Sprintf("%v: %v", pathErr.Path, pathErr.Err))
			}
			panic(err)
			//return false
		})
	}

	var wg sync.WaitGroup

	opts := SessionOptions{
		EnableFragmentation: enableFragmentation,
		NegotiatorOptions: NegotiatorOptions{
			Enable: NegotiatorEnableFalse,
		},
	}
	if maxPayloadSize > 0 {
		opts.PayloadSizeLimit = uint32(maxPayloadSize)
	}

	sess0 := identity0.NewSession(identity1, conn0, eventHandler, &opts)
	_ = sess0.Start(ctx)
	sess1 := identity1.NewSession(identity0, conn1, eventHandler, &opts)
	_ = sess1.Start(ctx)
	defer func() {
		cancelFunc()
		sess0.Close()
		sess1.Close()
		conn0.Close()
		conn1.Close()
		sess0.WaitForClosure()
		sess1.WaitForClosure()
		wg.Wait()
		b.StopTimer()
	}()

	writeBuf := make([]byte, blockSize)
	rand.Read(writeBuf)
	readBuf := make([]byte, blockSize)

	b.SetBytes(int64(blockSize))

	sendInfoChan := make(chan *SendInfo, 10000)
	if !isSync {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancelFunc()
			for {
				var sendInfo *SendInfo
				select {
				case <-ctx.Done():
					return
				case sendInfo = <-sendInfoChan:
				}

				select {
				case <-ctx.Done():
					return
				case <-sendInfo.Done():
				}
				if sendInfo.Err != nil {
					if !errors.As(sendInfo.Err, &ErrAlreadyClosed{}) && !errors.As(sendInfo.Err, &ErrCanceled{}) {
						panic(sendInfo.Err)
					}
				}
				sendInfo.Release()
			}
		}()

		sess1.SetHandlerFuncs(MessageTypeReadWrite,
			nil,
			func(err error) {
				panic(err)
			},
		)
	}

	b.ResetTimer()

	var err error
	var sendInfo *SendInfo
	for i := 0; i < b.N; i++ {

		// write

		if shouldWriteAsMessage {
			if isSync {
				_, err = sess0.WriteMessage(
					MessageTypeReadWrite,
					writeBuf,
				)
			} else {
				sendInfo = sess0.WriteMessageAsync(
					MessageTypeReadWrite,
					writeBuf,
				)
			}
		} else {
			_, err = sess0.Write(writeBuf)
		}
		if err != nil {
			panic(err)
		}

		// read

		if isSync {
			_, err = sess1.Read(readBuf)
		} else {
			if shouldWriteAsMessage {
				sendInfoChan <- sendInfo
			} else {
				panic(`!isSync && !shouldWriteAsMessage`)
			}
		}
		if err != nil {
			panic(err)
		}
	}

	select {
	case <-ctx.Done():
		panic(`should not happened`)
	default:
	}
}

func TestHackerDuplicateMessage(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{
		EnableDebug: true,
		KeyExchangerOptions: KeyExchangerOptions{
			AnswersMode:   KeyExchangeAnswersModeDisable,
			RetryInterval: time.Millisecond,
		},
	}

	// No hacker, yet

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	// Now a hacker appears, listens a message in the middle a repeats it.
	// A secureio client should ignore the duplicate

	// Intercepting a message

	// wait for successful key-exchange
	assert.Equal(t, SessionStateEstablished, sess0.WaitForState(ctx, SessionStateEstablished))
	assert.Equal(t, SessionStateEstablished, sess1.WaitForState(ctx, SessionStateEstablished))

	// sess1.SetPause(true) will temporary pause sess1.
	// So we will be able to read from `conn1` to intercept a message.
	assert.NoError(t, sess1.SetPause(true))

	msgSize := sess0.GetPayloadSizeLimit()

	// Now sess1 is paused (does not listen for traffic
	// and now we can intercept it), so sending a message:
	writeBuf := make([]byte, msgSize)
	rand.Read(writeBuf)
	_, err = sess0.Write(writeBuf)
	assert.NoError(t, err)

	// And intercepting it:
	interceptedMessage := make([]byte, sess1.GetPacketSizeLimit()+1)
	conn1.SetReadDeadline(time.Now().Add(time.Hour * 24 * 365))
	for {
		n, err := conn1.Read(interceptedMessage)
		if !assert.Less(t, n, int(sess1.GetPacketSizeLimit())+1) {
			return
		}
		if !assert.NoError(t, err) {
			return
		}
		if !assert.NotZero(t, n) {
			return
		}
		if n >= int(msgSize) { // waiting for our message
			interceptedMessage = interceptedMessage[:n]
			break
		}
	}

	// Unpausing and resending the message to pretend like we
	// weren't here:
	assert.Nil(t, sess1.SetPause(false))
	_, err = conn0.Write(interceptedMessage)
	assert.NoError(t, err)

	readBuf := make([]byte, msgSize+1)
	n, err := sess1.Read(readBuf)
	assert.NoError(t, err)
	if assert.Equal(t, int(msgSize), n) {
		readBuf = readBuf[:n]
	}

	assert.Equal(t, writeBuf, readBuf)

	successfullyIgnoredTheDuplicate := false
	assert.Equal(t, uint64(0), sess1.GetUnexpectedPacketIDCount())
	// And now repeating the message (making a duplicate).
	// This message should be ignored by "sess1" (if everything
	// works correctly and option AllowReorderingAndDuplication
	// is off):
	_, err = conn0.Write(interceptedMessage)
	assert.NoError(t, err)

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		slice.SetZeros(readBuf)
		_, err = sess1.Read(readBuf)
		assert.NoError(t, err)

		if !assert.Equal(t, uint64(1), sess1.GetUnexpectedPacketIDCount()) {
			// Unblocking the goroutine below (with `runtime.Gosched()`)
			// if required.
			*unsafetools.FieldByName(sess1, `unexpectedPacketIDCount`).(*uint64) = 1
		}
	}()

	go func() {
		for sess1.GetUnexpectedPacketIDCount() == 0 {
			runtime.Gosched()
		}
		successfullyIgnoredTheDuplicate = true

		// Just sending some message to unblock a goroutine above
		// (with `sess1.Read()`).
		_, err := sess0.Write([]byte{})
		assert.NoError(t, err)
	}()

	wg.Wait()
	assert.True(t, successfullyIgnoredTheDuplicate)

	// The test is passed, closing...

	assert.NoError(t, sess0.Close())
	assert.NoError(t, sess1.Close())

	waitForClosure(t, sess0, sess1)
}

func TestSessionID_Bytes(t *testing.T) {
	var id, idCmp SessionID
	id.CreatedAt = uint64(time.Now().UnixNano())
	id.Random = rand.Uint64()
	idBytes := id.Bytes()
	idCmp.FillFromBytes(idBytes[:])
	assert.Equal(t, id, idCmp)
}

func TestSession_WriteMessageAsync_sendNow(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{
		EnableDebug: true,
	}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	sess0.WaitForState(ctx, SessionStateEstablished)
	sess1.WaitForState(ctx, SessionStateEstablished)

	sendInfo := sess0.WriteMessageAsync(MessageTypeReadWrite, []byte{1})
	sendInfo.SendNowAndWait()
	assert.NotZero(t, sendInfo.N, sendInfo.SendID())
	select {
	case <-sendInfo.Done():
	default:
		assert.Fail(t, "sendInfo is not finished")
	}
	assert.NoError(t, sendInfo.Err)
	_ = sess0.CloseAndWait()
	_ = sess1.CloseAndWait()
}

func TestSession_uncovered(t *testing.T) {
	ctx := context.Background()
	identity0, identity1, _, _ := testPair(t)
	opts := &SessionOptions{
		EnableDebug:                        true,
		EnableInfo:                         true,
		ErrorOnSequentialDecryptFailsCount: &[]uint64{0}[0],
		SendDelay:                          &[]time.Duration{0}[0],
	}
	conn := newErroneousConn()
	sess0 := identity0.NewSession(identity1, conn, &testLogger{t}, opts)
	err := sess0.Start(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, SessionID{}, sess0.ID())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			errString := fmt.Sprint(recover())
			assert.True(t, strings.Index(errString, "should not happen") != -1, errString)
			wg.Done()
		}()
		writeBuf := make([]byte, 1)
		_, err := sess0.Write(writeBuf)
		assert.NoError(t, err)
	}()
	runtime.Gosched()
	close(*unsafetools.FieldByName(sess0, `waitForCipherKeyChan`).(*chan struct{}))
	wg.Wait()
	runtime.Gosched()
	conn.SetError(errors.New("unit-test"))
	runtime.Gosched()
	*unsafetools.FieldByName(sess0, `isReadingValue`).(*uint64) = 1
	_ = sess0.CloseAndWait()
}

func TestSession_WriteMessageTooBig(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, _ := testPair(t)

	opts := &SessionOptions{
		EnableDebug: true,
	}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	writeBuf := make([]byte, sess0.GetPayloadSizeLimit()*2)
	rand.Read(writeBuf)

	_, err = sess0.Write(writeBuf)
	assert.Error(t, err)
	_ = sess0.CloseAndWait()
}

func TestSession_answerModeMismatch(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())

	identity0, identity1, conn0, conn1 := testPair(t)

	opts0 := &SessionOptions{
		KeyExchangerOptions: KeyExchangerOptions{
			AnswersMode: KeyExchangeAnswersModeDisable,
		},
	}
	opts1 := &SessionOptions{
		KeyExchangerOptions: KeyExchangerOptions{
			AnswersMode: KeyExchangeAnswersModeAnswerAndWait,
		},
	}

	receivedMismatch := false
	var errorHandlerCallCount uint64
	errorHandler := func(sess *Session, err error) bool {
		atomic.AddUint64(&errorHandlerCallCount, 1)
		xerr := err.(*xerrors.Error)
		receivedMismatch = receivedMismatch || xerr.Has(ErrAnswersModeMismatch{})
		return false
	}
	sess0 := identity0.NewSession(identity1, conn0, wrapErrorHandler(nil, errorHandler), opts0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, wrapErrorHandler(nil, errorHandler), opts1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sess0.WaitForClosure()
		cancelFunc()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		sess1.WaitForClosure()
		cancelFunc()
	}()

	sess0.WaitForClosure()
	sess1.WaitForClosure()

	assert.True(t, receivedMismatch)
}

func TestSessionKeyRenew(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{
		EnableDebug: true,
		KeyExchangerOptions: KeyExchangerOptions{
			KeyUpdateInterval: time.Microsecond,
			RetryInterval:     time.Hour * 24 * 365, /* never */
			PSK:               []byte{1, 2, 3},
		},
	}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	for {
		if sess0.GetCipherKeysWait()[3] != nil {
			break
		}
		runtime.Gosched()
	}

	for {
		if sess1.GetCipherKeysWait()[3] != nil {
			break
		}
		runtime.Gosched()
	}

	sess0.CloseAndWait()
	sess1.CloseAndWait()
	runtime.Gosched()

	keys0 := sess0.GetCipherKeys()
	keys1 := sess1.GetCipherKeys()

	for i := 0; i < len(keys0); i++ {
		key0 := keys0[i]
		assert.NotNil(t, key0)
		for j := 0; j < len(keys1); j++ {
			key1 := keys1[j]
			assert.NotNil(t, key1)
			if bytes.Compare(key0, key1) == 0 {
				return
			}
		}
	}
	assert.Fail(t, "no key matches were found", fmt.Sprint(keys0, keys1))
}

func TestSession_SetHandlerFuncs(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	sess0.SetHandlerFuncs(MessageTypeChannel(0), func(i []byte) error {
		return nil
	}, func(err error) {
		return
	})
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	sess1.SetHandlerFuncs(MessageTypeChannel(0), func(i []byte) error {
		return nil
	}, func(err error) {
		return
	})
	err = sess1.Start(ctx)
	require.NoError(t, err)

	sess0.CloseAndWait()
	sess1.CloseAndWait()
}

func TestSession_StartClose(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	sess0.SetHandlerFuncs(MessageTypeChannel(0), func(i []byte) error {
		return nil
	}, func(err error) {
		return
	})
	require.NoError(t, sess0.Start(ctx))
	require.Error(t, sess0.Start(ctx))
	require.NoError(t, sess0.Close())
	require.Error(t, sess0.Start(ctx))
	require.Error(t, sess0.Close())
	require.NoError(t, conn1.Close())
}

func TestSession_Fragmentation(t *testing.T) {
	ctx := context.Background()

	identity0, identity1, conn0, conn1 := testPair(t)

	opts := &SessionOptions{
		EnableDebug:         true,
		EnableFragmentation: true,
		PayloadSizeLimit:    1000,
	}

	sess0 := identity0.NewSession(identity1, conn0, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess0)
	err := sess0.Start(ctx)
	require.NoError(t, err)

	sess1 := identity1.NewSession(identity0, conn1, &testLogger{t}, opts)
	printLogsOfSession(t, true, sess1)
	err = sess1.Start(ctx)
	require.NoError(t, err)

	writeBuf := make([]byte, 60000)
	rand.Read(writeBuf)
	readBuf := make([]byte, 60000)

	_, err = sess0.Write(writeBuf)
	assert.NoError(t, err)

	_, err = sess1.Read(readBuf)
	assert.NoError(t, err)

	assert.Equal(t, writeBuf, readBuf)

	assert.NoError(t, sess0.Close())
	assert.NoError(t, sess1.Close())

	waitForClosure(t, sess0, sess1)
}
