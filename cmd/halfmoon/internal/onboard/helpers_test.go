package onboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyEmbeddedToTargetUsesStructuredAgentFiles(t *testing.T) {
	targetDir := t.TempDir()

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	agentPath := filepath.Join(targetDir, "AGENT.md")
	if _, err := os.Stat(agentPath); err != nil {
		t.Fatalf("expected %s to exist: %v", agentPath, err)
	}

	soulPath := filepath.Join(targetDir, "SOUL.md")
	if _, err := os.Stat(soulPath); err != nil {
		t.Fatalf("expected %s to exist: %v", soulPath, err)
	}

	userPath := filepath.Join(targetDir, "USER.md")
	if _, err := os.Stat(userPath); err != nil {
		t.Fatalf("expected %s to exist: %v", userPath, err)
	}

	for _, legacyName := range []string{"AGENTS.md", "IDENTITY.md"} {
		legacyPath := filepath.Join(targetDir, legacyName)
		if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
			t.Fatalf("expected legacy file %s to be absent, got err=%v", legacyPath, err)
		}
	}
}

func TestCopyEmbeddedToTargetPreservesExistingFiles(t *testing.T) {
	targetDir := t.TempDir()

	// Write a custom AGENT.md before onboarding
	customContent := "# My Custom Agent\nThis should survive onboard."
	agentPath := filepath.Join(targetDir, "AGENT.md")
	if err := os.WriteFile(agentPath, []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	// Custom content must be preserved
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customContent {
		t.Fatalf("AGENT.md was overwritten: got %q, want %q", string(data), customContent)
	}
}

func TestCopyEmbeddedToTargetCreatesNewFiles(t *testing.T) {
	targetDir := t.TempDir()

	// Pre-create only AGENT.md with custom content
	customContent := "# Custom Agent"
	if err := os.WriteFile(filepath.Join(targetDir, "AGENT.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	// AGENT.md should keep custom content
	data, err := os.ReadFile(filepath.Join(targetDir, "AGENT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customContent {
		t.Fatalf("AGENT.md was overwritten: got %q", string(data))
	}

	// Missing files should be created from templates
	for _, name := range []string{"SOUL.md", "USER.md", "memory/MEMORY.md"} {
		path := filepath.Join(targetDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to be created: %v", name, err)
		}
	}
}

func TestCopyEmbeddedToTargetPreservesMemory(t *testing.T) {
	targetDir := t.TempDir()

	// Pre-create memory directory and file
	memDir := filepath.Join(targetDir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customMemory := "# Memory\n- Remember: user prefers dark mode"
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte(customMemory), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(memDir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customMemory {
		t.Fatalf("memory/MEMORY.md was overwritten: got %q, want %q", string(data), customMemory)
	}
}

func TestCopyEmbeddedToTargetPreservesSkills(t *testing.T) {
	targetDir := t.TempDir()

	// Pre-create a skill that also exists in embedded templates
	skillDir := filepath.Join(targetDir, "skills", "summarize")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customSkill := "# Custom Summarize Skill\nModified by user."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(customSkill), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customSkill {
		t.Fatalf("skills/summarize/SKILL.md was overwritten: got %q, want %q", string(data), customSkill)
	}
}
