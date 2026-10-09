package api

import (
	"encoding/json"
	"testing"
)

// A contract's spec carries its highest leverage (max_leverage), the bound
// of HOUSE's contract leverage (review C73); a pair's carries none.
func TestASpecCarriesTheContractsLeverage(t *testing.T) {
	var row instrumentRow
	body := `{"symbol":"BTC-USDT-PERP","base_asset":"BTC","quote_asset":"USDT","tick_size":"0.1","lot_size":"0.001",
		"status":"TRADING","reference_symbol":"BTCUSDT","settle_asset":"USDT","contract_size":"0","max_leverage":150}`
	if err := json.Unmarshal([]byte(body), &row); err != nil {
		t.Fatal(err)
	}
	s, ok := row.spec(true)
	if !ok || !s.Contract || s.MaxLeverage != 150 || s.SettleAsset() != "USDT" {
		t.Fatalf("contract %+v %v", s, ok)
	}
	if s, ok := row.spec(false); !ok || s.MaxLeverage != 0 {
		t.Fatalf("pair %+v %v", s, ok)
	}
}
