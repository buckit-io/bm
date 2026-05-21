package preflight

import "time"

func unixNanoNow() int64 {
	return time.Now().UnixNano()
}
