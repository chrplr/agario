// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agario/internal/agarienv"
)

// build compiles the server once for the tests in this file. Driving the real
// binary over real pipes is the only way to catch a missing flush, which no
// in-process test can see.
func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agario-env")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestBinaryAnswersOverPipesAndExitsOnEOF(t *testing.T) {
	cmd := exec.Command(build(t), "-food", "40", "-bots", "2", "-viruses", "2")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr // inherited, never piped: an undrained pipe deadlocks
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	out := bufio.NewReader(stdout)
	ask := func(req string) map[string]any {
		t.Helper()
		if _, err := stdin.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		// This read is the flush test: without a per-response flush on the Go
		// side it blocks here until the buffer happens to fill.
		line, err := out.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		if ok, _ := m["ok"].(bool); !ok {
			t.Fatalf("request %s failed: %s", req, line)
		}
		return m
	}

	hello := ask(`{"id":1,"cmd":"hello"}`)
	meta, _ := hello["meta"].(map[string]any)
	if meta == nil {
		t.Fatal("hello carried no meta")
	}
	if meta["game"] != "agario" {
		t.Errorf("game = %v, want agario", meta["game"])
	}
	// The flags must reach the handshake, or the client sizes its spaces wrong.
	if meta["food"].(float64) != 40 || meta["bots"].(float64) != 2 {
		t.Errorf("flags did not reach the handshake: food=%v bots=%v", meta["food"], meta["bots"])
	}

	ask(`{"id":2,"cmd":"reset","env_id":0,"seed":7}`)
	for i := 0; i < 5; i++ {
		ask(`{"id":3,"cmd":"step","env_id":0,"action":[1,0]}`)
	}

	// Closing stdin alone must stop the process: this is the backstop that
	// prevents orphans when a Python client dies without saying close.
	stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("did not exit when stdin closed")
	}
}

func TestBinaryRejectsNonsenseFlags(t *testing.T) {
	bin := build(t)
	for _, args := range [][]string{
		{"-frames", "0"},
		{"-k-food", "0"},
		{"-view-scale", "0"},
		{"-bots", "-1"},
	} {
		cmd := exec.Command(bin, args...)
		if err := cmd.Run(); err == nil {
			t.Errorf("%v was accepted, expected a non-zero exit", args)
		}
	}
}

func TestValidateAcceptsTheDefaults(t *testing.T) {
	if err := validate(agarienv.DefaultOptions()); err != nil {
		t.Errorf("the defaults do not validate: %v", err)
	}
}
