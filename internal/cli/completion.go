package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for lifecycle.

To load completions:

Bash:
  $ source <(lifecycle completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ lifecycle completion bash > /etc/bash_completion.d/lifecycle
  # macOS:
  $ lifecycle completion bash > $(brew --prefix)/etc/bash_completion.d/lifecycle

Zsh:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc  # if not already enabled
  $ lifecycle completion zsh > "${fpath[1]}/_lifecycle"

Fish:
  $ lifecycle completion fish | source
  $ lifecycle completion fish > ~/.config/fish/completions/lifecycle.fish

PowerShell:
  PS> lifecycle completion powershell | Out-String | Invoke-Expression
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return cmd.Root().GenBashCompletion(os.Stdout)
		case "zsh":
			return cmd.Root().GenZshCompletion(os.Stdout)
		case "fish":
			return cmd.Root().GenFishCompletion(os.Stdout, true)
		case "powershell":
			return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
		}
		return nil
	},
}
