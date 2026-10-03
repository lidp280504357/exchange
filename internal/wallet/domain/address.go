package domain

import (
	"bytes"
	"crypto/sha256"
	"math/big"
	"strings"

	"github.com/lidp280504357/exchange/internal/platform/evm"
)

// Address formats of networks (instrument-service's address_format).
const (
	FormatEVM  = "EVM"
	FormatTRON = "TRON"
	FormatBTC  = "BTC"
)

// Why an address is refused.
const (
	ReasonAddressFormat   = "ADDRESS_FORMAT"   // not an address of the network's kind
	ReasonAddressChecksum = "ADDRESS_CHECKSUM" // the checksum does not match (a typo)
	ReasonAddressNetwork  = "ADDRESS_NETWORK"  // an address of another network, e.g. testnet on mainnet
	ReasonMemoRequired    = "MEMO_REQUIRED"    // the network needs a memo or tag
	ReasonAddressOwn      = "ADDRESS_OWN"      // the user's own deposit address
	ReasonAddressRetired  = "ADDRESS_RETIRED"  // a retired deposit address of the custodian's stand-in, no chain's
)

// AddressCheck is the outcome of checking an address.
type AddressCheck struct {
	Valid bool
	// Normalized is the address as the network writes it (EIP-55 case for
	// EVM); empty when invalid.
	Normalized string
	// Reason is one of the Reason codes when invalid.
	Reason string
}

func refused(reason string) AddressCheck { return AddressCheck{Reason: reason} }

// CheckAddress checks an address's format for a network: EVM addresses
// (0x and 40 hex digits, EIP-55 when mixed case), TRON (Base58Check, 0x41
// prefix) and Bitcoin (bech32/bech32m segwit, Base58Check P2PKH/P2SH) on
// the mainnet, testnet or regtest its chain names. It only checks the
// form: it cannot tell whether anybody holds the address.
func CheckAddress(n Network, address, memo string) AddressCheck {
	address = strings.TrimSpace(address)
	if n.MemoRequired && strings.TrimSpace(memo) == "" {
		return refused(ReasonMemoRequired)
	}
	switch n.AddressFormat {
	case FormatTRON:
		return checkTRON(address)
	case FormatBTC:
		return checkBTC(address, btcParamsOf(n.Chain))
	default:
		return checkEVM(address)
	}
}

func checkEVM(a string) AddressCheck {
	if len(a) != 42 || !strings.HasPrefix(a, "0x") {
		return refused(ReasonAddressFormat)
	}
	if !evm.ValidAddress(a) {
		if evm.ValidAddress(strings.ToLower(a)) {
			return refused(ReasonAddressChecksum)
		}
		return refused(ReasonAddressFormat)
	}
	return AddressCheck{Valid: true, Normalized: evm.Checksum(a)}
}

func checkTRON(a string) AddressCheck {
	if len(a) != 34 || a[0] != 'T' {
		return refused(ReasonAddressFormat)
	}
	payload, reason := base58Check(a)
	switch {
	case reason != "":
		return refused(reason)
	case len(payload) != 21 || payload[0] != 0x41:
		return refused(ReasonAddressFormat)
	}
	return AddressCheck{Valid: true, Normalized: a}
}

// btcParams are the address prefixes of one Bitcoin network.
type btcParams struct {
	hrp        string
	p2pkh      byte
	p2sh       byte
	otherHRPs  []string
	otherBytes []byte
}

var (
	btcMain    = btcParams{hrp: "bc", p2pkh: 0x00, p2sh: 0x05, otherHRPs: []string{"tb", "bcrt"}, otherBytes: []byte{0x6f, 0xc4}}
	btcTest    = btcParams{hrp: "tb", p2pkh: 0x6f, p2sh: 0xc4, otherHRPs: []string{"bc", "bcrt"}, otherBytes: []byte{0x00, 0x05}}
	btcRegtest = btcParams{hrp: "bcrt", p2pkh: 0x6f, p2sh: 0xc4, otherHRPs: []string{"bc", "tb"}, otherBytes: []byte{0x00, 0x05}}
)

// btcParamsOf reads the network from the chain name: bitcoin (mainnet),
// bitcoin-testnet or bitcoin-signet, bitcoin-regtest.
func btcParamsOf(chain string) btcParams {
	c := strings.ToLower(chain)
	switch {
	case strings.Contains(c, "regtest"):
		return btcRegtest
	case strings.Contains(c, "test"), strings.Contains(c, "signet"):
		return btcTest
	}
	return btcMain
}

func checkBTC(a string, p btcParams) AddressCheck {
	if i := strings.LastIndexByte(a, '1'); i > 0 && (strings.HasPrefix(strings.ToLower(a), p.hrp+"1") || hasAnyPrefix(strings.ToLower(a), p.otherHRPs)) {
		hrp, version, program, reason := decodeSegwit(a)
		switch {
		case reason != "":
			return refused(reason)
		case hrp != p.hrp:
			return refused(ReasonAddressNetwork)
		case version == 0 && len(program) != 20 && len(program) != 32:
			return refused(ReasonAddressFormat)
		}
		return AddressCheck{Valid: true, Normalized: strings.ToLower(a)}
	}
	if len(a) < 26 || len(a) > 35 {
		return refused(ReasonAddressFormat)
	}
	payload, reason := base58Check(a)
	switch {
	case reason != "":
		return refused(reason)
	case len(payload) != 21:
		return refused(ReasonAddressFormat)
	case payload[0] == p.p2pkh || payload[0] == p.p2sh:
		return AddressCheck{Valid: true, Normalized: a}
	case bytes.IndexByte(p.otherBytes, payload[0]) >= 0:
		return refused(ReasonAddressNetwork)
	}
	return refused(ReasonAddressFormat)
}

func hasAnyPrefix(s string, hrps []string) bool {
	for _, h := range hrps {
		if strings.HasPrefix(s, h+"1") {
			return true
		}
	}
	return false
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Check decodes s and verifies its 4-byte double-SHA256 checksum,
// returning the payload.
func base58Check(s string) ([]byte, string) {
	n := new(big.Int)
	radix := big.NewInt(58)
	for _, c := range s {
		i := strings.IndexRune(base58Alphabet, c)
		if i < 0 {
			return nil, ReasonAddressFormat
		}
		n.Mul(n, radix).Add(n, big.NewInt(int64(i)))
	}
	decoded := n.Bytes()
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	decoded = append(make([]byte, zeros), decoded...)
	if len(decoded) < 5 {
		return nil, ReasonAddressFormat
	}
	payload, sum := decoded[:len(decoded)-4], decoded[len(decoded)-4:]
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	if !bytes.Equal(second[:4], sum) {
		return nil, ReasonAddressChecksum
	}
	return payload, ""
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// Checksum constants of bech32 (witness version 0) and bech32m (1-16),
// BIP-173 and BIP-350.
const (
	bech32Const  = 1
	bech32mConst = 0x2bc830a3
)

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func hrpExpand(hrp string) []byte {
	out := make([]byte, 0, 2*len(hrp)+1)
	for i := range len(hrp) {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := range len(hrp) {
		out = append(out, hrp[i]&31)
	}
	return out
}

// decodeSegwit decodes a segwit address into its human-readable part,
// witness version and program.
func decodeSegwit(a string) (string, int, []byte, string) {
	if len(a) > 90 || (strings.ToLower(a) != a && strings.ToUpper(a) != a) {
		return "", 0, nil, ReasonAddressFormat
	}
	a = strings.ToLower(a)
	sep := strings.LastIndexByte(a, '1')
	if sep < 1 || sep+7 > len(a) {
		return "", 0, nil, ReasonAddressFormat
	}
	hrp, data := a[:sep], make([]byte, 0, len(a)-sep-1)
	for _, c := range a[sep+1:] {
		i := strings.IndexRune(bech32Charset, c)
		if i < 0 {
			return "", 0, nil, ReasonAddressFormat
		}
		data = append(data, byte(i)) //nolint:gosec // an index into the 32-letter charset
	}
	if len(data) < 7 {
		return "", 0, nil, ReasonAddressFormat
	}
	version := int(data[0])
	want := uint32(bech32Const)
	if version > 0 {
		want = bech32mConst
	}
	if version > 16 {
		return "", 0, nil, ReasonAddressFormat
	}
	if bech32Polymod(append(hrpExpand(hrp), data...)) != want {
		return "", 0, nil, ReasonAddressChecksum
	}
	program, ok := convertBits(data[1:len(data)-6], 5, 8)
	if !ok || len(program) < 2 || len(program) > 40 {
		return "", 0, nil, ReasonAddressFormat
	}
	return hrp, version, program, ""
}

// convertBits regroups 5-bit words into bytes without padding.
func convertBits(data []byte, from, to uint) ([]byte, bool) {
	acc, bits := uint(0), uint(0)
	maxv := uint(1)<<to - 1
	var out []byte
	for _, v := range data {
		acc = acc<<from | uint(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxv)) //nolint:gosec // masked to the target width (8 bits)
		}
	}
	if bits >= from || (acc<<(to-bits))&maxv != 0 {
		return nil, false
	}
	return out, true
}
