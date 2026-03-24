// Halfmoon - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Halfmoon contributors

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/agent"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/auth"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/cron"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/gateway"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/migrate"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/model"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/onboard"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/skills"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/status"
	"github.com/halfmoon-labs/halfmoon/cmd/halfmoon/internal/version"
	"github.com/halfmoon-labs/halfmoon/pkg/config"
)

func NewHalfmoonCommand() *cobra.Command {
	short := fmt.Sprintf("%s halfmoon - Personal AI Assistant v%s\n\n", internal.Logo, config.GetVersion())

	cmd := &cobra.Command{
		Use:     "halfmoon",
		Short:   short,
		Example: "halfmoon version",
	}

	cmd.AddCommand(
		onboard.NewOnboardCommand(),
		agent.NewAgentCommand(),
		auth.NewAuthCommand(),
		gateway.NewGatewayCommand(),
		status.NewStatusCommand(),
		cron.NewCronCommand(),
		migrate.NewMigrateCommand(),
		skills.NewSkillsCommand(),
		model.NewModelCommand(),
		version.NewVersionCommand(),
	)

	return cmd
}

const (
	colorPurple = "\033[1;38;2;138;92;199m"
	colorBlue   = "\033[1;38;2;62;93;185m"
	banner      = "\r\n" +
		colorPurple + "██╗  ██╗ █████╗ ██╗     ███████╗" + colorBlue + "███╗   ███╗ ██████╗  ██████╗ ███╗   ██╗\n" +
		colorPurple + "██║  ██║██╔══██╗██║     ██╔════╝" + colorBlue + "████╗ ████║██╔═══██╗██╔═══██╗████╗  ██║\n" +
		colorPurple + "███████║███████║██║     █████╗  " + colorBlue + "██╔████╔██║██║   ██║██║   ██║██╔██╗ ██║\n" +
		colorPurple + "██╔══██║██╔══██║██║     ██╔══╝  " + colorBlue + "██║╚██╔╝██║██║   ██║██║   ██║██║╚██╗██║\n" +
		colorPurple + "██║  ██║██║  ██║███████╗██║     " + colorBlue + "██║ ╚═╝ ██║╚██████╔╝╚██████╔╝██║ ╚████║\n" +
		colorPurple + "╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝╚═╝     " + colorBlue + "╚═╝     ╚═╝ ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝\n " +
		"\033[0m\r\n"
)

func main() {
	fmt.Printf("%s", banner)
	cmd := NewHalfmoonCommand()
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
