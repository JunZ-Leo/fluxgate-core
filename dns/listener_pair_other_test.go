//go:build !windows

package dns

import "syscall"

var dnsPairRetryErrors = []error{syscall.EADDRINUSE, syscall.EACCES}
