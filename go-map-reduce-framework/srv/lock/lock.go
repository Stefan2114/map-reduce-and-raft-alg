package lock

import (
	"go-map-reduce-framework/srv/kvtest"
	"go-map-reduce-framework/srv/rpc"
	"time"
)

type Lock struct {
	// IKVClerk is a go interface for k/v clerks: the interface hides
	// the specific Clerk type of ck but promises that ck supports
	// Put and Get.  The tester passes the clerk in when calling
	// MakeLock().
	ck  kvtest.IKVClerk
	key string
	id  string
}

// The tester calls MakeLock() and passes in a k/v clerk; your code can
// perform a Put or Get by calling lk.ck.Put() or lk.ck.Get().
//
// Use l as the key to store the "lock state" (you would have to decide
// precisely what the lock state is).
func MakeLock(ck kvtest.IKVClerk, l string) *Lock {
	id := kvtest.RandValue(8)
	lk := &Lock{ck: ck, key: l, id: id}
	lk.ck.Put(lk.key, "free", 0)

	return lk
}

func (lk *Lock) Acquire() {
	for {
		val, ver, err := lk.ck.Get(lk.key)

		if err == rpc.OK && val == lk.id {
			return
		}

		if err == rpc.OK && val == "free" {
			putErr := lk.ck.Put(lk.key, lk.id, ver)

			if putErr == rpc.OK {
				return
			}
		}

		// 3. BACKOFF: Don't hammer the server while waiting
		time.Sleep(50 * time.Millisecond)
	}
}

func (lk *Lock) Release() {
	for {
		val, ver, err := lk.ck.Get(lk.key)

		if err == rpc.OK && val != lk.id {
			return
		}

		if err == rpc.OK && val == lk.id {
			putErr := lk.ck.Put(lk.key, "free", ver)
			if putErr == rpc.OK {
				return
			}
		}

		time.Sleep(10 * time.Millisecond)
	}
}
