package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"webdavmount/internal/updatefeed"
)

// Only the independent update verification key is compiled in. Operator key
// material remains outside the project, protected by current-user Windows DPAPI.
const updatePublicKeyHex = "9359795949700eedaf5233968777b4efaccb5c076ed0138c43e776d7813d9303"

func compiledUpdatePolicy() *DesktopUpdatePolicy {
	publicKey, err := hex.DecodeString(updatePublicKeyHex)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil
	}
	return &DesktopUpdatePolicy{
		FeedURL:       "https://github.com/makessr/WebDAVMount/releases/download/stable-v2/latest.json",
		MirrorOrigins: []string{"https://gh-proxy.com", "https://ghproxy.net"},
		Policy: updatefeed.Policy{
			Product:          "WebDAVMount",
			PublicKey:        ed25519.PublicKey(publicKey),
			AllowedHosts:     []string{"github.com"},
			GitHubRepository: "makessr/WebDAVMount",
			MaxBytes:         256 << 20,
		},
	}
}
