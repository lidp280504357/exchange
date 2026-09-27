// Package protodef embeds the Protobuf sources that producers register
// with Schema Registry.
package protodef

import _ "embed" // for go:embed

// EnvelopeProto is the source of exchange.event.v1.Envelope, registered as
// the value schema of every event topic.
//
//go:embed exchange/event/v1/envelope.proto
var EnvelopeProto string
