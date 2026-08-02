package incusx

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// verboseLogger returns the `[verbose] incus api: …` sink for w. Incus calls are
// fanned out across goroutines (see listV2Machines), so the sink is serialised:
// without the lock two concurrent calls race on the writer, which is a real data
// race whenever w is a buffer rather than os.Stderr.
func verboseLogger(w io.Writer) func(string) {
	var mu sync.Mutex
	return func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, msg)
	}
}

func logIncusAPICall[T any](log func(string), label string, fn func() (T, error)) (T, error) {
	if log == nil {
		return fn()
	}
	start := time.Now()
	result, err := fn()
	if err != nil {
		log(fmt.Sprintf("[verbose] incus api: %s failed (%s)\n", label, formatVerboseDuration(time.Since(start))))
		return result, err
	}
	log(fmt.Sprintf("[verbose] incus api: %s done (%s)\n", label, formatVerboseDuration(time.Since(start))))
	return result, nil
}

func logIncusAPICall0(log func(string), label string, fn func() error) error {
	if log == nil {
		return fn()
	}
	start := time.Now()
	if err := fn(); err != nil {
		log(fmt.Sprintf("[verbose] incus api: %s failed (%s)\n", label, formatVerboseDuration(time.Since(start))))
		return err
	}
	log(fmt.Sprintf("[verbose] incus api: %s done (%s)\n", label, formatVerboseDuration(time.Since(start))))
	return nil
}
