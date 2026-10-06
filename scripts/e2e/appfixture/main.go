// Command appfixture writes the apps admin.sh uploads to the console (design
// 2026-10-07, App download page, batch H1): a minimal .apk (a zip with a
// compiled AndroidManifest.xml naming its package, versions and minimum
// SDK), an .ipa (a zip with Payload/E2E.app/Info.plist) and a
// .mobileconfig (a configuration profile), and prints each one's path,
// size and SHA-256 as JSON. Nothing in them runs.
//
//	go run ./scripts/e2e/appfixture -out DIR [-version 1.0.0]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skill/exchange/internal/admin/adapters/apppkg/apppkgtest"
)

func main() {
	out := flag.String("out", "", "the directory to write the files to")
	version := flag.String("version", "1.0.0", "the apps' version")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: appfixture -out DIR [-version V]")
		os.Exit(2)
	}
	files := map[string][]byte{
		"apk":          apppkgtest.APK("vip.astras.e2e", *version, 1, 24),
		"ipa":          apppkgtest.IPA("vip.astras.e2e", *version, "1", "15.0"),
		"mobileconfig": apppkgtest.Mobileconfig(),
	}
	result := map[string]any{}
	for kind, data := range files {
		path := filepath.Join(*out, "e2e."+kind)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sum := sha256.Sum256(data)
		result[kind] = map[string]any{"path": path, "size": len(data), "sha256": hex.EncodeToString(sum[:])}
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
