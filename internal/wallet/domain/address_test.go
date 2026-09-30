package domain

import "testing"

func TestCheckAddress(t *testing.T) {
	evmNet := Network{Network: "ETH-SEPOLIA", Chain: "11155111", AddressFormat: FormatEVM}
	tron := Network{Network: "TRX", Chain: "tron", AddressFormat: FormatTRON}
	btc := Network{Network: "BTC", Chain: "bitcoin", AddressFormat: FormatBTC}
	btcTest := Network{Network: "BTC-TESTNET", Chain: "bitcoin-testnet", AddressFormat: FormatBTC}
	tagged := Network{Network: "XRP", Chain: "xrp", AddressFormat: FormatEVM, MemoRequired: true}
	cases := []struct {
		name    string
		n       Network
		address string
		reason  string // "" when valid
		norm    string
	}{
		{"evm checksummed", evmNet, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", "", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{"evm lower case", evmNet, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{"evm bad checksum", evmNet, "0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", ReasonAddressChecksum, ""},
		{"evm short", evmNet, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1bea", ReasonAddressFormat, ""},
		{"evm no prefix", evmNet, "5aaeb6053f3e94c9b9a09f33669435e7ef1beaed00", ReasonAddressFormat, ""},
		{"tron", tron, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"},
		{"tron typo", tron, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj7t", ReasonAddressChecksum, ""},
		{"tron evm address", tron, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", ReasonAddressFormat, ""},
		{"btc p2wpkh", btc, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", "", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"},
		{"btc upper case", btc, "BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4", "", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"},
		{"btc v1 bech32m", btc, "bc1pw508d6qejxtdg4y5r3zarvary0c5xw7kw508d6qejxtdg4y5r3zarvary0c5xw7kt5nd6y", "", "bc1pw508d6qejxtdg4y5r3zarvary0c5xw7kw508d6qejxtdg4y5r3zarvary0c5xw7kt5nd6y"},
		{"btc v1 with bech32", btc, "bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqh2y7hd", ReasonAddressChecksum, ""},
		{"btc v0 with bech32m", btc, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kemeawh", ReasonAddressChecksum, ""},
		{"btc typo", btc, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5", ReasonAddressChecksum, ""},
		{"btc mixed case", btc, "bc1qW508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", ReasonAddressFormat, ""},
		{"btc p2pkh", btc, "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"},
		{"btc p2sh", btc, "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", "", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy"},
		{"btc testnet on mainnet", btc, "tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", ReasonAddressNetwork, ""},
		{"btc testnet base58 on mainnet", btc, "mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", ReasonAddressNetwork, ""},
		{"btc testnet", btcTest, "tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", "", "tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7"},
		{"btc evm address", btc, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", ReasonAddressFormat, ""},
		{"memo missing", tagged, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", ReasonMemoRequired, ""},
	}
	for _, c := range cases {
		got := CheckAddress(c.n, c.address, "")
		if got.Valid != (c.reason == "") || got.Reason != c.reason || got.Normalized != c.norm {
			t.Errorf("%s: %+v, want reason %q normalized %q", c.name, got, c.reason, c.norm)
		}
	}
	if got := CheckAddress(tagged, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "12345"); !got.Valid {
		t.Errorf("memo given: %+v", got)
	}
}
