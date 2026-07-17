package main

import (
	"encoding/json"
	"io"
	"os"
)

func toJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func readJSONFile(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func writeStdout(v interface{}) {
	json.NewEncoder(os.Stdout).Encode(v)
}

// jsonDecoder creates a JSON decoder for any reader.
func jsonDecoder(r io.Reader) *json.Decoder {
	return json.NewDecoder(r)
}
