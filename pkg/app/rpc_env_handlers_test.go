package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/tools"
)

func cleanupTestEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		name := name
		t.Cleanup(func() { _ = os.Unsetenv(name) })
	}
}

func testAppWithEnv() *App {
	return &App{envInjector: tools.NewEnvInjector()}
}

func TestHandleEnvSetGetListUnset(t *testing.T) {
	const (
		nameOne = "AI_TEST_ENV_ONE"
		nameTwo = "AI_TEST_ENV_TWO"
	)
	cleanupTestEnv(t, nameOne, nameTwo)

	app := testAppWithEnv()

	result, err := app.handleEnv("set " + nameOne + " aaa")
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if got := fmt.Sprint(result); got != "set "+nameOne {
		t.Fatalf("set result = %q, want %q", got, "set "+nameOne)
	}
	// The agent process environment must not be modified.
	if _, ok := os.LookupEnv(nameOne); ok {
		t.Fatalf("%s leaked into the agent process environment", nameOne)
	}
	if got, ok := app.envInjector.Lookup(nameOne); !ok || got != "aaa" {
		t.Fatalf("injected %s = %q, present=%v, want aaa", nameOne, got, ok)
	}

	// A value containing spaces must survive parsing intact.
	if _, err := app.handleEnv("set " + nameTwo + " two words here"); err != nil {
		t.Fatalf("set with spaces failed: %v", err)
	}
	if got, ok := app.envInjector.Lookup(nameTwo); !ok || got != "two words here" {
		t.Fatalf("injected %s = %q, present=%v, want %q", nameTwo, got, ok, "two words here")
	}
	if _, ok := os.LookupEnv(nameTwo); ok {
		t.Fatalf("%s leaked into the agent process environment", nameTwo)
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
	if _, ok := app.envInjector.Lookup(nameOne); ok {
		t.Errorf("%s still visible to bash after unset", nameOne)
	}
	if _, ok := os.LookupEnv(nameOne); ok {
		t.Errorf("%s leaked into the agent process environment after unset", nameOne)
	}
	gotAfterUnset, err := app.handleEnv("get " + nameOne)
	if err != nil {
		t.Fatalf("get after unset failed: %v", err)
	}
	if want := nameOne + " is not set"; gotAfterUnset != want {
		t.Fatalf("get after unset = %q, want %q", gotAfterUnset, want)
	}
}

func TestHandleEnvListOnlyShowsOperatorEntries(t *testing.T) {
	const (
		inherited = "AI_TEST_ENV_INHERITED_FOR_LIST"
		setName   = "AI_TEST_ENV_SET_FOR_LIST"
		unsetName = "AI_TEST_ENV_UNSET_FOR_LIST"
	)
	cleanupTestEnv(t, inherited, setName, unsetName)
	if err := os.Setenv(inherited, "process-secret"); err != nil {
		t.Fatalf("set inherited env: %v", err)
	}
	if err := os.Setenv(unsetName, "hidden-secret"); err != nil {
		t.Fatalf("set env to hide: %v", err)
	}

	app := testAppWithEnv()
	if _, err := app.handleEnv("set " + setName + " operator-value"); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if _, err := app.handleEnv("unset " + unsetName); err != nil {
		t.Fatalf("unset failed: %v", err)
	}

	listed, err := app.handleEnv("list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	listText := fmt.Sprint(listed)
	if strings.Contains(listText, "process-secret") || strings.Contains(listText, inherited+"=") {
		t.Errorf("list leaked an inherited variable: %q", listText)
	}
	if !strings.Contains(listText, setName+"=operator-value") {
		t.Errorf("list missing operator value: %q", listText)
	}
	if !strings.Contains(listText, unsetName+"=<unset>") {
		t.Errorf("list missing unset marker: %q", listText)
	}
	if strings.Contains(listText, "hidden-secret") {
		t.Errorf("list leaked the value of an unset variable: %q", listText)
	}
}

func TestHandleEnvUnsetHidesProcessEnvForBashOnly(t *testing.T) {
	const name = "AI_TEST_ENV_FROM_PROCESS"
	cleanupTestEnv(t, name)
	if err := os.Setenv(name, "process-value"); err != nil {
		t.Fatalf("set process env: %v", err)
	}

	app := testAppWithEnv()
	if _, err := app.handleEnv("unset " + name); err != nil {
		t.Fatalf("unset failed: %v", err)
	}

	// The agent process keeps its variable; only bash's view loses it.
	if got := os.Getenv(name); got != "process-value" {
		t.Fatalf("agent process %s = %q, want process-value", name, got)
	}
	if _, ok := app.envInjector.Lookup(name); ok {
		t.Errorf("%s still visible to bash after unset", name)
	}
	for _, entry := range app.envInjector.Environ() {
		if strings.HasPrefix(entry, name+"=") {
			t.Errorf("bash env still contains %s: %q", name, entry)
		}
	}
}

func TestHandleEnvSetDoesNotEchoValue(t *testing.T) {
	const name = "AI_TEST_ENV_SECRET"
	cleanupTestEnv(t, name)

	app := testAppWithEnv()
	result, err := app.handleEnv("set " + name + " bbb")
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if strings.Contains(fmt.Sprint(result), "bbb") {
		t.Errorf("set result echoed the secret value: %v", result)
	}
}

func TestHandleEnvRejectsMalformedInput(t *testing.T) {
	app := testAppWithEnv()
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

	app := &App{commands: command.New(), envInjector: tools.NewEnvInjector()}
	app.registerEnvHandlers()

	result, err := app.handlePrompt("/env set "+name+" value", false, "")
	if err != nil {
		t.Fatalf("slash dispatch failed: %v", err)
	}
	if got, ok := app.envInjector.Lookup(name); !ok || got != "value" {
		t.Fatalf("injected %s = %q, present=%v, want value", name, got, ok)
	}
	if _, ok := os.LookupEnv(name); ok {
		t.Fatalf("%s leaked into the agent process environment", name)
	}
	if got := fmt.Sprint(result); got != "set "+name {
		t.Fatalf("result = %q, want %q", got, "set "+name)
	}
	// If the slash text had reached ag.Prompt, the nil agent would have panicked.
}
func TestHandleEnvGetEmptyValueRendersFeedback(t *testing.T) {
	const name = "AI_TEST_ENV_EMPTY"
	cleanupTestEnv(t, name)
	if err := os.Setenv(name, ""); err != nil {
		t.Fatalf("set empty process env: %v", err)
	}

	got, err := testAppWithEnv().handleEnv("get " + name)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if want := name + " is set to an empty value"; got != want {
		t.Fatalf("get = %q, want %q", got, want)
	}
}
