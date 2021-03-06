package secureio

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// SendInfo contains information about the scheduled sending request.
// It's values should be read only after "<-(*SendInfo).Done()" will finish.
type SendInfo struct {
	// Err contains the resulting error
	Err error

	// N contains the number of bytes were written to send the merged
	// messaged through the backend.
	N int

	sendID   uint64 // for debug only
	c        chan struct{}
	ctx      context.Context
	refCount int64
	isBusy   bool
	sess     *Session
	pool     *sendInfoPool
}

var (
	nextSendID uint64
)

type sendInfoPool struct {
	storage sync.Pool
}

func newSendInfoPool(sess *Session) *sendInfoPool {
	pool := &sendInfoPool{}
	pool.storage = sync.Pool{
		New: func() interface{} {
			return &SendInfo{
				c:      make(chan struct{}),
				sendID: atomic.AddUint64(&nextSendID, 1),

				sess: sess,
				pool: pool,
			}
		},
	}
	return pool
}

func (pool *sendInfoPool) AcquireSendInfo(ctx context.Context) *SendInfo {
	sendInfo := pool.storage.Get().(*SendInfo)
	if sendInfo.isBusy {
		panic(`should not happened`)
	}
	sendInfo.isBusy = true
	sendInfo.incRefCount()
	sendInfo.c = make(chan struct{})
	sendInfo.sendID = atomic.AddUint64(&nextSendID, 1)
	sendInfo.ctx = ctx
	return sendInfo
}

func (pool *sendInfoPool) Put(freeSendInfo *SendInfo) {
	if !freeSendInfo.isBusy {
		panic(fmt.Sprintf(`should not happened (isBusy == %v)`,
			freeSendInfo.isBusy))
	}
	freeSendInfo.isBusy = false
	freeSendInfo.reset()
	pool.storage.Put(freeSendInfo)
}

// Done returns a channel which should be used to wait until
// a real sending will be performed. After that values
// `SendInfo.Err` and `SendInfo.N` could be read and method
// `(*SendInfo).Release()` could be called.
func (sendInfo *SendInfo) Done() <-chan struct{} {
	return sendInfo.c
}

// SendID returns the unique ID of the sending request. It could be
// called at any moment before `(*SendInfo).Release()`.
func (sendInfo *SendInfo) SendID() uint64 {
	return sendInfo.sendID
}

func (sendInfo *SendInfo) reset() {
	sendInfo.Err = nil
	sendInfo.N = 0
}

func (sendInfo *SendInfo) incRefCount() int64 {
	return atomic.AddInt64(&sendInfo.refCount, 1)
}

func (sendInfo SendInfo) duplicate() interface{} {
	return &sendInfo
}

// Release just puts the `*SendInfo` back to the memory pool
// to be re-used in future. It could be used to reduce the pressure on GC.
// It's **NOT** necessary to call this function. It is supposed to be used
// only high-performant applications.
func (sendInfo *SendInfo) Release() {
	select {
	case <-sendInfo.c:
	case <-sendInfo.ctx.Done():
	default:
		panic("Release() was called on a non-finished sendInfo")
	}
	refCount := atomic.AddInt64(&sendInfo.refCount, -1)
	if refCount > 0 {
		return
	}
	if refCount < 0 {
		panic(fmt.Sprintf(`should not happened (refCount == %v)`,
			refCount))
	}
	sendInfo.pool.Put(sendInfo)
}

func (sendInfo *SendInfo) String() string {
	return fmt.Sprintf("{c: %v; Err: %v: N: %v: sendID: %v, refCount: %v}",
		sendInfo.c, sendInfo.Err, sendInfo.N, sendInfo.sendID, atomic.LoadInt64(&sendInfo.refCount))
}

// SendNowAndWait belays the rest part of the send delay of the remaining send iteration
// and forces to send the data ASAP and wait until it will be done.
func (sendInfo *SendInfo) SendNowAndWait() {
	if sendInfo.incRefCount() == 1 {
		panic("should not happen")
	}
	sendInfo.sess.sendDelayedNowChan <- sendInfo
	sendInfo.Wait()
}

// Wait waits until message is send or until the context is cancelled
// (for example if session is closed).
func (sendInfo *SendInfo) Wait() {
	select {
	case <-sendInfo.Done():
	case <-sendInfo.ctx.Done():
	}
}
