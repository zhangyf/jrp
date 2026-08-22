package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// runEncryptEnv encrypts the plaintext .env into .env.enc (AES-256-GCM), keyed
// to this machine/user/skillDir so it can be read back by `jrp` and cos_node.mjs.
// The old .env.enc (if any) is backed up with a timestamp before overwrite.
//
// This command does not need --lang: it only touches local files and must work
// even when the existing .env.enc is undecryptable (the situation it fixes).
func runEncryptEnv(fs *flag.FlagSet, lang string) {
	fs.Parse(cmdArgs)

	skillDir := cosSkillDir()
	envPath := filepath.Join(skillDir, ".env")
	encPath := filepath.Join(skillDir, ".env.enc")

	plaintext, err := os.ReadFile(envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read %s: %v\n", envPath, err)
		os.Exit(1)
	}
	if len(plaintext) == 0 {
		fmt.Fprintf(os.Stderr, "Error: %s is empty\n", envPath)
		os.Exit(1)
	}

	enc, err := encryptEnvFile(string(plaintext), skillDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: encrypt failed: %v\n", err)
		os.Exit(1)
	}

	// Back up the existing .env.enc before overwrite.
	if _, err := os.Stat(encPath); err == nil {
		bak := encPath + ".bak." + time.Now().Format("20060102_150405")
		if err := os.Rename(encPath, bak); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot back up old .env.enc: %v\n", err)
			os.Exit(1)
		}
	}

	if err := os.WriteFile(encPath, enc, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write %s: %v\n", encPath, err)
		os.Exit(1)
	}

	// Round-trip verification: re-read and decrypt with the same key.
	back, err := os.ReadFile(encPath)
	if err == nil {
		if _, derr := decryptEnvFile(back, skillDir); derr != nil {
			fmt.Fprintf(os.Stderr, "Error: round-trip verification failed: %v\n", derr)
			os.Exit(1)
		}
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"action":    "encrypt-env",
		"encPath":   encPath,
		"bytes":     len(enc),
		"roundTrip": true,
	})
}
