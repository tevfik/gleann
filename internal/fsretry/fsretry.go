// Package fsretry replaces a file by rename, riding out the short window in
// which another handle on the target makes Windows refuse the rename.
//
// On Linux and macOS os.Rename over a file that a reader has open always
// succeeds. On Windows it fails with ERROR_ACCESS_DENIED or
// ERROR_SHARING_VIOLATION while any goroutine or process holds the target
// open without FILE_SHARE_DELETE (a metadata or config read in progress is
// enough). The holder lets go within milliseconds, so a bounded retry makes the
// replace succeed; unlike a lock inside this process, it also covers readers
// in other processes. Elsewhere the retry never triggers.
package fsretry

import (
	"os"
	"time"
)

const (
	// budget bounds the total time spent waiting for the target to be released.
	budget = time.Second
	// firstDelay is the first backoff step; it doubles up to maxDelay.
	firstDelay = time.Millisecond
	maxDelay   = 50 * time.Millisecond
)

// Rename is os.Rename that retries, for up to about a second, while the
// failure is a transient sharing error (Windows only). The last error is
// returned when the budget runs out.
func Rename(oldpath, newpath string) error {
	return rename(os.Rename, isTransient, time.Sleep, budget, oldpath, newpath)
}

// ReadFile is os.ReadFile with the same retry. On Windows a file that is being
// replaced by a rename cannot be opened for a moment (access denied / sharing
// violation); a reader that gives up there turns a harmless race into an error.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := retry(func() (err error) { data, err = os.ReadFile(path); return }, isTransient, time.Sleep, budget)
	return data, err
}

// rename is Rename with its dependencies injected: renameFn does the rename,
// transient classifies an error as worth retrying, sleep waits, and total is
// the time budget (counted in the delays handed to sleep, so tests run
// instantly).
func rename(renameFn func(string, string) error, transient func(error) bool, sleep func(time.Duration), total time.Duration, oldpath, newpath string) error {
	return retry(func() error { return renameFn(oldpath, newpath) }, transient, sleep, total)
}

// retry runs op until it succeeds, fails with a non-transient error, or the
// delays handed to sleep add up to total; the last error is returned.
func retry(op func() error, transient func(error) bool, sleep func(time.Duration), total time.Duration) error {
	delay := firstDelay
	var waited time.Duration
	for {
		err := op()
		if err == nil || !transient(err) || waited >= total {
			return err
		}
		if delay > total-waited {
			delay = total - waited
		}
		sleep(delay)
		waited += delay
		if delay *= 2; delay > maxDelay {
			delay = maxDelay
		}
	}
}

// IsBusy reports whether err is the error Windows returns for a file another
// handle holds open (ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION). It is
// always false on other systems, where an open file can be removed.
func IsBusy(err error) bool { return isTransient(err) }
