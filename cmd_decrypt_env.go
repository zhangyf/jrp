package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// runDecryptEnv prints (or writes out) the plaintext .env that is currently
// stored encrypted in .env.enc. It is the counterpart of `encrypt-env` and
// exists so credentials can be *migrated* out of a directory that is about to
// be replaced (e.g. a marketplace-managed skill dir):
//
//	# read the old location (default: ~/.workbuddy/skills/tencentcloud-cos)
//	jrp decrypt-env --out ~/.workbuddy/cos-credentials/.env
//
//	# then re-encrypt under the new location
//	JRP_COS_SKILL_DIR=~/.workbuddy/cos-credentials jrp encrypt-env
//
// The key is derived from the skill dir, so a .env.enc can only be decrypted
// by pointing JRP_COS_SKILL_DIR at the directory it was encrypted for.
// Secret values are written to a file rather than stdout by default because
// stdout is frequently captured into logs; `--print` forces stdout output.
func runDecryptEnv(fs *flag.FlagSet, lang string) {
	outPath := fs.String("out", "", "Write plaintext .env to this path (recommended; 0600)")
	printOut := fs.Bool("print", false, "Also print plaintext to stdout (leaks into logs)")
	fs.Parse(cmdArgs)

	skillDir := cosSkillDir()
	encPath := filepath.Join(skillDir, ".env.enc")

	enc, err := os.ReadFile(encPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read %s: %v\n", encPath, err)
		os.Exit(1)
	}

	plaintext, err := decryptEnvFile(enc, skillDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: decrypt failed (wrong JRP_COS_SKILL_DIR?): %v\n", err)
		os.Exit(1)
	}

	if *outPath != "" {
		if err := os.MkdirAll(filepath.Dir(*outPath), 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot create dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*outPath, []byte(plaintext), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot write %s: %v\n", *outPath, err)
			os.Exit(1)
		}
	}

	if *printOut {
		fmt.Print(plaintext)
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"action":   "decrypt-env",
		"skillDir": skillDir,
		"encPath":  encPath,
		"outPath":  *outPath,
		"bytes":    len(plaintext),
	})
}
