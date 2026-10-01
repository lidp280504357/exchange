package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"

	"github.com/lidp280504357/exchange/internal/platform/evm"
)

// Chains by Udun's main coin type: Bitcoin, TRON; everything else is
// taken for an EVM chain (Ethereum 60, BNB Smart Chain 9006, ...).
const (
	chainBTC  = "0"
	chainTRON = "195"
)

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// newAddress makes a well-formed address of the chain: bech32 P2WPKH on
// Bitcoin's mainnet, Base58Check on TRON, EIP-55 elsewhere.
func newAddress(mainCoinType string) string {
	switch mainCoinType {
	case chainBTC:
		return bech32("bc", 0, random(20))
	case chainTRON:
		return base58Check(append([]byte{0x41}, random(20)...))
	default:
		return evm.Checksum("0x" + hex.EncodeToString(random(20)))
	}
}

// newTxID makes a transaction hash in the chain's notation.
func newTxID(mainCoinType string) string {
	h := hex.EncodeToString(random(32))
	if mainCoinType == chainBTC || mainCoinType == chainTRON {
		return h
	}
	return "0x" + h
}

var (
	evmRE    = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	tronRE   = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	btcRE    = regexp.MustCompile(`^(bc1[02-9ac-hj-np-z]{11,71}|[13][1-9A-HJ-NP-Za-km-z]{25,34})$`)
	base58Al = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
)

// validAddress is the gateway's address check: the chain's form, without
// its checksum.
func validAddress(mainCoinType, address string) bool {
	switch mainCoinType {
	case chainBTC:
		return btcRE.MatchString(strings.ToLower(address)) || btcRE.MatchString(address)
	case chainTRON:
		return tronRE.MatchString(address)
	default:
		return evmRE.MatchString(address)
	}
}

func base58Check(payload []byte) string {
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	data := append(append([]byte{}, payload...), second[:4]...)
	n := new(big.Int).SetBytes(data)
	base, mod := big.NewInt(58), new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, base58Al[mod.Int64()])
	}
	for _, b := range data {
		if b != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

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

// bech32 encodes a witness v0 program (BIP-173).
func bech32(hrp string, version byte, program []byte) string {
	data := []byte{version}
	acc, bits := 0, 0
	for _, b := range program {
		acc = acc<<8 | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			data = append(data, byte(acc>>bits&31))
		}
	}
	if bits > 0 {
		data = append(data, byte(acc<<(5-bits)&31))
	}
	expanded := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for _, c := range []byte(hrp) {
		expanded = append(expanded, c>>5)
	}
	expanded = append(expanded, 0)
	for _, c := range []byte(hrp) {
		expanded = append(expanded, c&31)
	}
	expanded = append(expanded, data...)
	expanded = append(expanded, 0, 0, 0, 0, 0, 0)
	mod := bech32Polymod(expanded) ^ 1
	var sb strings.Builder
	sb.WriteString(hrp + "1")
	for _, d := range data {
		sb.WriteByte(bech32Charset[d])
	}
	for i := range 6 {
		sb.WriteByte(bech32Charset[(mod>>uint(5*(5-i)))&31])
	}
	return sb.String()
}
