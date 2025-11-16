package cmd

import (
	"context"
	"fmt"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	"github.com/omnitrix-sh/cli/internals/app"
	"github.com/omnitrix-sh/cli/internals/config"
	"github.com/omnitrix-sh/cli/internals/database"
	"github.com/omnitrix-sh/cli/internals/format"
	agent "github.com/omnitrix-sh/cli/internals/llm/agents"
	"github.com/omnitrix-sh/cli/internals/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "omnitrix.sh",
	Short: "cursor for terminal",
	Long:  `a claude cli alternative with no vendor lock-in`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flag("help").Changed {
			cmd.Help()
			return nil
		}
	debug, _ := cmd.Flags().GetBool("debug")
	prompt, _ := cmd.Flags().GetString("prompt")
	outputFormat, _ := cmd.Flags().GetString("output-format")
	quiet, _ := cmd.Flags().GetBool("quiet")

	viper.Set("debug", debug)
	if debug {
		viper.Set("log.level", "debug")
	}

	// Validate format option
	if !format.IsValid(outputFormat) {
		return fmt.Errorf("invalid format option: %s\n%s", outputFormat, format.GetHelpText())
	}

	// Load configuration before connecting to database
	if err := config.Load(debug); err != nil {
		return err
	}

	conn, err := database.Connect()
	if err != nil {
		return err
	}
	ctx := context.Background()

	appInstance, err := app.New(ctx, conn)
	if err != nil {
		return err
	}
	defer appInstance.Close()

	// Non-interactive mode
	if prompt != "" {
		appInstance.Logger.Info("Running non-interactive mode", "prompt_length", len(prompt))
		return appInstance.RunNonInteractive(ctx, prompt, outputFormat, quiet)
	}

	// Interactive mode (TUI)
	appInstance.Logger.Info("Starting omnitrix...")
	zone.NewGlobal()
	tuiProgram := tea.NewProgram(
		tui.New(appInstance),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	appInstance.Logger.Info("Setting up subscriptions...")
	ch, unsub := setupSubscriptions(appInstance)
	defer unsub()

	go func() {
		agent.GetMcpTools(ctx)
		for msg := range ch {
			tuiProgram.Send(msg)
		}
	}()
	if _, err := tuiProgram.Run(); err != nil {
		return err
	}
	return nil
	},
}

func setupSubscriptions(app *app.App) (chan tea.Msg, func()) {
	ch := make(chan tea.Msg)
	wg := sync.WaitGroup{}
	ctx, cancel := context.WithCancel(app.Context)

	{
		sub := app.Logger.Subscribe(ctx)
		wg.Add(1)
		go func() {
			for ev := range sub {
				ch <- ev
			}
			wg.Done()
		}()
	}
	{
		sub := app.Sessions.Subscribe(ctx)
		wg.Add(1)
		go func() {
			for ev := range sub {
				ch <- ev
			}
			wg.Done()
		}()
	}
	{
		sub := app.Messages.Subscribe(ctx)
		wg.Add(1)
		go func() {
			for ev := range sub {
				ch <- ev
			}
			wg.Done()
		}()
	}
	{
		sub := app.Permissions.Subscribe(ctx)
		wg.Add(1)
		go func() {
			for ev := range sub {
				ch <- ev
			}
			wg.Done()
		}()
	}
	{
		sub := app.Status.Subscribe(ctx)
		wg.Add(1)
		go func() {
			for ev := range sub {
				ch <- ev
			}
			wg.Done()
		}()
	}

	return ch, func() {
		cancel()
		wg.Wait()
		close(ch)
	}
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolP("help", "h", false, "Help")
	rootCmd.Flags().BoolP("debug", "d", false, "Enable debug mode")
	rootCmd.Flags().StringP("prompt", "p", "", "Prompt to run in non-interactive mode")
	rootCmd.Flags().StringP("output-format", "f", "text", "Output format for non-interactive mode (text, json)")
	rootCmd.Flags().BoolP("quiet", "q", false, "Hide spinner in non-interactive mode")
}
