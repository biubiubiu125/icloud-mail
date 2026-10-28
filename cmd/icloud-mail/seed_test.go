package main

import "testing"

func TestExistingAdminIgnoresShortSeedPassword(t *testing.T) {
	if err := validateAdminSeed("x", "short", 1); err != nil {
		t.Fatal(err)
	}
	if err := validateAdminSeed("x", "short", 0); err == nil {
		t.Fatal("short seed must fail when no admin exists")
	}
	if err := validateAdminSeed("admin", "long-enough", 0); err != nil {
		t.Fatal(err)
	}
	if err := validateAdminSeed("", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := validateAdminSeed("admin", "", 0); err == nil {
		t.Fatal("partial seed must fail when creating")
	}
}
