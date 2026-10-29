package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForFreshStoreCodeDiscardsPreChallengeFileAndConsumesFreshCode(t *testing.T) {
	dir := t.TempDir()
	path := storeCodeHandoffPath(dir)
	if err := os.WriteFile(path, []byte("111111"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		code, err := waitForFreshStoreCode(ctx, dir)
		result <- code
		errs <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pre-challenge handoff was not removed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(path, []byte("654321\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != "654321" {
		t.Fatalf("unexpected code %q", code)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("consumed handoff still exists")
	}
}

func TestWaitForFreshStoreCodeRejectsInvalidCodeAndDeletesIt(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := waitForFreshStoreCode(ctx, dir)
		result <- err
	}()
	time.Sleep(50 * time.Millisecond)
	path := filepath.Join(dir, storeCodeHandoffName)
	if err := os.WriteFile(path, []byte("12ab56"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("invalid code was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid handoff still exists")
	}
}
