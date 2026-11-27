package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/command"
)

func cleanupTestEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		name := name
		t.Cleanup(func() { _ = os.Unsetenv(name) })
	}
}

func TestHandleEnvSetGetListUnset(t *testing.T) {
	const (
		nameOne = "AI_TEST_ENV_ONE"
		nameTwo = "AI_TEST_ENV_TWO"
	)
	cleanupTestEnv(t, nameOne, nameTwo)

	app := &App{}

	result, err := app.handleEnv("set " + nameOne + " aaa")
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if got := fmt.Sprint(result); got != "set "+nameOne {
		t.Fatalf("set result = %q, want %q", got, "set "+nameOne)
	}
	if got := os.Getenv(nameOne); got != "aaa" {
		t.Fatalf("%s = %q, want aaa", nameOne, got)
	}

	// A value containing spaces must survive parsing intact.
	if _, err := app.handleEnv("set " + nameTwo + " two words here"); err != nil {
		t.Fatalf("set with spaces failed: %v", err)
	}
	if got := os.Getenv(nameTwo); got != "two words here" {
		t.Fatalf("%s = %q, want %q", nameTwo, got, "two words here")
	}

	got, err := app.handleEnv("get " + nameOne)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got != "aaa" {
		t.Fatalf("get = %v, want aaa", got)
	}

	listed, err := app.handleEnv("list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	listText := fmt.Sprint(listed)
	for _, want := range []string{nameOne + "=aaa", nameTwo + "=two words here"} {
		if !strings.Contains(listText, want) {
			t.Errorf("list output missing %q", want)
		}
	}

	if _, err := app.handleEnv("unset " + nameOne); err != nil {
		t.Fatalf("unset failed: %v", err)
	}
	if _, ok := os.LookupEnv(nameOne); ok {
		t.Errorf("%s still present after unset", nameOne)
	}
}

func TestHandleEnvSetDoesNotEchoValue(t *testing.T) {
	const name = "AI_TEST_ENV_SECRET"
	cleanupTestEnv(t, name)

	app := &App{}
	result, err := app.handleEnv("set " + name + " bbb")
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if strings.Contains(fmt.Sprint(result), "bbb") {
		t.Errorf("set result echoed the secret value: %v", result)
	}
}

func TestHandleEnvRejectsMalformedInput(t *testing.T) {
	app := &App{}
	for _, args := range []string{
		"",
		"bogus",
		"set NO_VALUE",
		"set BAD=NAME x",
		"get",
		"get BAD=NAME",
		"unset",
		"unset BAD=NAME",
	} {
		if _, err := app.handleEnv(args); err == nil {
			t.Errorf("handleEnv(%q) unexpectedly succeeded", args)
		}
	}
}

func TestEnvSlashCommandStaysOutOfAgentTurn(t *testing.T) {
	const name = "AI_TEST_ENV_PROMPT"
	cleanupTestEnv(t, name)

	app := &App{commands: command.New()}
	app.registerEnvHandlers()

	result, err := app.handlePrompt("/env set "+name+" value", false, "")
	if err != nil {
		t.Fatalf("slash dispatch failed: %v", err)
	}
	if got := os.Getenv(name); got != "value" {
		t.Fatalf("%s = %q, want value", name, got)
	}
	if got := fmt.Sprint(result); got != "set "+name {
		t.Fatalf("result = %q, want %q", got, "set "+name)
	}
	// If the slash text had reached ag.Prompt, the nil agent would have panicked.
}
