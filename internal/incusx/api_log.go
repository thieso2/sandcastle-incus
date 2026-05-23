package incusx

import (
	"fmt"
	"time"
)

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
