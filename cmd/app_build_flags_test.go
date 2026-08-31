package cmd

import (
	"testing"
)

// The build flags exist because the API has supported build_mode and
// build_strategy on RegisterAppInput since #867, but the CLI never exposed
// them -- so every app a user registered was pinned to the ci_pushed default
// and the platform's own in-cluster builder was unreachable from the terminal.

func TestApplyBuildFlagsOmitsEverythingWhenNothingIsAsked(t *testing.T) {
	input := map[string]interface{}{}
	if err := applyBuildFlags(input, "", "", "", ""); err != nil {
		t.Fatalf("applyBuildFlags: %v", err)
	}
	if len(input) != 0 {
		t.Fatalf("expected no keys so the platform keeps its own defaults, got %v", input)
	}
}

func TestPlatformBuildImpliesTheDockerfileStrategy(t *testing.T) {
	input := map[string]interface{}{}
	if err := applyBuildFlags(input, "platform_build", "", "", ""); err != nil {
		t.Fatalf("applyBuildFlags: %v", err)
	}
	if input["buildMode"] != "platform_build" {
		t.Errorf("buildMode = %v, want platform_build", input["buildMode"])
	}
	// platform_build with strategy 'off' selects no builder at all, so the
	// build activity has nothing to invoke and the app can never ship an image.
	if input["buildStrategy"] != "dockerfile" {
		t.Errorf("buildStrategy = %v, want dockerfile implied", input["buildStrategy"])
	}
}

func TestAnExplicitStrategySurvivesTheImplication(t *testing.T) {
	input := map[string]interface{}{}
	if err := applyBuildFlags(input, "platform_build", "buildpacks", "", ""); err != nil {
		t.Fatalf("applyBuildFlags: %v", err)
	}
	if input["buildStrategy"] != "buildpacks" {
		t.Errorf("buildStrategy = %v, want buildpacks", input["buildStrategy"])
	}
}

func TestCiPushedDoesNotGainAStrategy(t *testing.T) {
	input := map[string]interface{}{}
	if err := applyBuildFlags(input, "ci_pushed", "", "", ""); err != nil {
		t.Fatalf("applyBuildFlags: %v", err)
	}
	if _, ok := input["buildStrategy"]; ok {
		t.Errorf("ci_pushed must not imply a builder, got %v", input["buildStrategy"])
	}
}

func TestDockerfileAndContextArePassedThrough(t *testing.T) {
	input := map[string]interface{}{}
	err := applyBuildFlags(input, "platform_build", "dockerfile", "svc/api/Dockerfile", "svc/api")
	if err != nil {
		t.Fatalf("applyBuildFlags: %v", err)
	}
	if input["dockerfilePath"] != "svc/api/Dockerfile" {
		t.Errorf("dockerfilePath = %v", input["dockerfilePath"])
	}
	if input["buildContext"] != "svc/api" {
		t.Errorf("buildContext = %v", input["buildContext"])
	}
}

func TestUnknownValuesAreRejectedBeforeTheRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, mode, strategy string }{
		{"bad mode", "platform-build", ""},
		{"bad strategy", "", "kaniko"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]interface{}{}
			if err := applyBuildFlags(input, tc.mode, tc.strategy, "", ""); err == nil {
				t.Fatal("expected a client-side rejection, got nil")
			}
			if len(input) != 0 {
				t.Errorf("a rejected call must not half-populate the input: %v", input)
			}
		})
	}
}

// The enum lists are duplicated from the platform, so they can drift. These
// pin the values that exist today; a platform change has to update both.
func TestEnumsMatchThePlatformChoices(t *testing.T) {
	wantModes := map[string]bool{"ci_pushed": true, "platform_build": true, "none": true}
	if len(validBuildModes) != len(wantModes) {
		t.Fatalf("validBuildModes = %v", validBuildModes)
	}
	for _, m := range validBuildModes {
		if !wantModes[m] {
			t.Errorf("unexpected build mode %q", m)
		}
	}
	wantStrategies := map[string]bool{"off": true, "dockerfile": true, "buildpacks": true, "nixpacks": true}
	if len(validBuildStrategies) != len(wantStrategies) {
		t.Fatalf("validBuildStrategies = %v", validBuildStrategies)
	}
	for _, s := range validBuildStrategies {
		if !wantStrategies[s] {
			t.Errorf("unexpected build strategy %q", s)
		}
	}
}

func TestRegisterExposesTheBuildFlags(t *testing.T) {
	for _, name := range []string{"build-mode", "build-strategy", "dockerfile-path", "build-context"} {
		if appRegisterCmd.Flags().Lookup(name) == nil {
			t.Errorf("astro app register is missing --%s", name)
		}
	}
}
