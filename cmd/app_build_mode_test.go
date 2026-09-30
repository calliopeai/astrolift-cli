package cmd

import (
	"testing"
)

// `updateApp` has accepted a buildMode None-sentinel since build modes were
// introduced, but no CLI command called it -- `--build-mode` existed only on
// `astro app register`. So an app's build mode was fixed at registration for
// life, and an operator whose platform builder could not produce the image (a
// source repo it cannot clone, no builder on the cluster) had no route out
// short of deregistering the app and tearing down its cloud resources.

func TestSetBuildModeAlwaysSendsAStrategyAlongsideTheMode(t *testing.T) {
	// The whole reason this differs from applyBuildFlags on register.
	// DeployAppWorkflow gates the build on `build_strategy != "off"` and never
	// reads build_mode, so an app registered as platform_build keeps its saved
	// `dockerfile` strategy and goes on building after a mode-only flip. The
	// row would read ci_pushed while every deploy still ran the builder.
	input, err := buildModeUpdateInput("app-guid", "ci_pushed", "")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if input["buildMode"] != "ci_pushed" {
		t.Errorf("buildMode = %v, want ci_pushed", input["buildMode"])
	}
	if input["buildStrategy"] != "off" {
		t.Errorf("buildStrategy = %v, want off -- a mode-only flip does not stop the builder",
			input["buildStrategy"])
	}
}

func TestSetBuildModeToNoneAlsoDisablesTheBuilder(t *testing.T) {
	// `none` means the app ships no image at all, so it must not leave a
	// strategy behind that would still select a builder.
	input, err := buildModeUpdateInput("app-guid", "none", "")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if input["buildStrategy"] != "off" {
		t.Errorf("buildStrategy = %v, want off", input["buildStrategy"])
	}
}

func TestSetBuildModeToPlatformBuildImpliesDockerfile(t *testing.T) {
	// Mirrors applyBuildFlags: platform_build with strategy off selects no
	// builder, so the build activity has nothing to invoke.
	input, err := buildModeUpdateInput("app-guid", "platform_build", "")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if input["buildStrategy"] != "dockerfile" {
		t.Errorf("buildStrategy = %v, want dockerfile implied", input["buildStrategy"])
	}
}

func TestSetBuildModeExplicitStrategyWins(t *testing.T) {
	input, err := buildModeUpdateInput("app-guid", "platform_build", "buildpacks")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if input["buildStrategy"] != "buildpacks" {
		t.Errorf("buildStrategy = %v, want buildpacks", input["buildStrategy"])
	}
}

func TestSetBuildModeOverrideCanForceABuilderOffUnderPlatformBuild(t *testing.T) {
	// An explicit `off` is allowed to contradict the implication: the operator
	// asked for it by name, and refusing would make the flag a half-flag.
	input, err := buildModeUpdateInput("app-guid", "platform_build", "off")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if input["buildStrategy"] != "off" {
		t.Errorf("buildStrategy = %v, want the explicit off to win", input["buildStrategy"])
	}
}

func TestSetBuildModeCarriesTheAppID(t *testing.T) {
	// updateApp is keyed by guid; the command resolves the slug the operator
	// typed into one before calling this.
	input, err := buildModeUpdateInput("01a0596e-3f97-7fd2-998d-61cc63efcdfc", "ci_pushed", "")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if input["id"] != "01a0596e-3f97-7fd2-998d-61cc63efcdfc" {
		t.Errorf("id = %v, want the guid passed in", input["id"])
	}
}

func TestSetBuildModeRejectsAnUnknownMode(t *testing.T) {
	if _, err := buildModeUpdateInput("app-guid", "dockerfile", ""); err == nil {
		t.Fatal("expected an error: dockerfile is a strategy, not a mode")
	}
}

func TestSetBuildModeRejectsAnUnknownStrategy(t *testing.T) {
	if _, err := buildModeUpdateInput("app-guid", "platform_build", "kaniko"); err == nil {
		t.Fatal("expected an error for an unknown strategy")
	}
}

func TestSetBuildModeSendsNothingItWasNotAskedFor(t *testing.T) {
	// Every other field on UpdateAppInput is a None-sentinel, so sending one
	// would overwrite a value the operator never mentioned -- notably the
	// Dockerfile path and build context saved at registration.
	input, err := buildModeUpdateInput("app-guid", "ci_pushed", "")
	if err != nil {
		t.Fatalf("buildModeUpdateInput: %v", err)
	}
	if len(input) != 3 {
		t.Fatalf("expected exactly id + buildMode + buildStrategy, got %v", input)
	}
}
