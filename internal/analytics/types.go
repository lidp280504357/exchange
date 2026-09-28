package analytics

// Link the event payload types into the binary, so that PayloadJSON can
// render them instead of keeping raw bytes.
import (
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"        // registers audit event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"         // registers auth event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1" // registers notification event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"         // registers user event types
)
