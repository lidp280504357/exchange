package analytics

// Link the event payload types into the binary, so that PayloadJSON can
// render them instead of keeping raw bytes.
import (
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/account/v1"      // registers account event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"        // registers audit event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"         // registers auth event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"   // registers instrument event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"       // registers ledger event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1" // registers notification event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"        // registers order event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"         // registers risk event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"        // registers trade event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"         // registers user event types
	_ "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"       // registers wallet event types
)
