package dns

import "golang.org/x/sys/windows"

var dnsPairRetryErrors = []error{windows.WSAEADDRINUSE, windows.WSAEACCES}
