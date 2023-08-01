package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v53/github"
	"github.com/gregjones/httpcache"
	"github.com/palantir/go-githubapp/githubapp"
	"github.com/pkg/errors"
	"github.com/rcrowley/go-metrics"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v2"
)

type PRBranchUpdateHandler struct {
	githubapp.ClientCreator
	preamble string
}

type Config struct {
	Server HTTPConfig       `yaml:"server"`
	Github githubapp.Config `yaml:"github"`

	AppConfig MyApplicationConfig `yaml:"app_configuration"`
}

type MyApplicationConfig struct {
	PullRequestPreamble string `yaml:"pull_request_preamble"`
}

type HTTPConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

func readConfig(path string) (*Config, error) {
	var c Config

	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed reading server config file: %s", path)
	}

	if err := yaml.UnmarshalStrict(bytes, &c); err != nil {
		return nil, errors.Wrap(err, "failed parsing configuration file")
	}

	return &c, nil
}

func (h *PRBranchUpdateHandler) Handles() []string {
	return []string{"push"}
}

// This handler is called when the server recives a webhook event for a push to the default branch
// The handler will then update all open pull requests that are behind the default branch
func (h *PRBranchUpdateHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	var pushEvent *github.PushEvent
	if err := json.Unmarshal(payload, &pushEvent); err != nil {
		return errors.Wrap(err, "failed to parse push event payload")
	}

	// Get the installation ID
	installationID := githubapp.GetInstallationIDFromEvent(pushEvent)

	// Get the installation client
	client, err := h.NewInstallationClient(installationID)
	if err != nil {
		return err
	}

	// Get the repository information
	repo := pushEvent.GetRepo()
	repoOwner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()
	repoDefaultBranch := repo.GetDefaultBranch()

	// Check if the push was to the default branch
	if pushEvent.GetRef() != fmt.Sprintf("refs/heads/%s", repoDefaultBranch) {
		return nil
	}

	// Get the default branch
	defaultBranch, _, err := client.Repositories.GetBranch(ctx, repoOwner, repoName, repo.GetDefaultBranch(), true)
	if err != nil {
		return err
	}

	// Get the latest commit on the default branch
	//defaultBranchSha := defaultBranch.GetCommit().GetSHA()

	// Get all open pull requests
	fmt.Printf("Getting all open pull requests for %s/%s\n", repoOwner, repoName)
	pullRequests, _, err := client.PullRequests.List(ctx, repoOwner, repoName, &github.PullRequestListOptions{
		State: "open",
	})
	if err != nil {
		return err
	}

	fmt.Printf("Found %d open pull requests\n", len(pullRequests))

	// Iterate over all open pull requests
	for _, pr := range pullRequests {
		//get the pull request information
		prNum := pr.GetNumber()
		headRef := pr.GetHead().GetRef()
		baseRef := pr.GetBase().GetRef()

		// Compare the pull request head to the default branch
		commitComparison, _, _ := client.Repositories.CompareCommits(ctx, repoOwner, repoName, baseRef, headRef, nil)

		fmt.Printf("Pull request %s/%s#%d is behind default branch %s by %d commits\n", repoOwner, repoName, prNum, repoDefaultBranch, commitComparison.GetBehindBy())

		// Check if the pull request is behind the default branch
		if commitComparison.GetBehindBy() >= 1 {
			fmt.Printf("Pull request %s/%s#%d is behind default branch %s\n", repoOwner, repoName, prNum, repoDefaultBranch)
			// update the pull request
			updateResponse, _, err := client.PullRequests.UpdateBranch(ctx, repoOwner, repoName, prNum, nil)
			if err != nil {
				// Check if the error is due to the job being scheduled on GitHub side
				if err.Error() == "job scheduled on GitHub side; try again later" {
					fmt.Printf("Job scheduled on GitHub side\n")

					// Comment on the pull request
					msg := fmt.Sprintf("%s\n\n%s", h.preamble, updateResponse.GetMessage())
					prComment := github.IssueComment{
						Body: &msg,
					}
					fmt.Printf("Commenting on pull request %s/%s#%d\n", repoOwner, repoName, prNum)

					if _, _, err := client.Issues.CreateComment(ctx, repoOwner, repoName, prNum, &prComment); err != nil {
						return err
					}
				} else {
					// Comment on the pull request that the update failed
					msg := fmt.Sprintf("Failed to update pull request. Error: %s", err.Error())
					prComment := github.IssueComment{
						Body: &msg,
					}
					fmt.Printf("Commenting on pull request %s/%s#%d\n", repoOwner, repoName, prNum)
					if _, _, err := client.Issues.CreateComment(ctx, repoOwner, repoName, prNum, &prComment); err != nil {
						return err
					}
					return err
				}
			}
			fmt.Printf("Updated pull request %s/%s#%d. Message: %s\n", repoOwner, repoName, prNum, updateResponse.GetMessage())
		} else {
			fmt.Printf("Pull request %s/%s#%d on branch %s is up to date with default branch %s\n", repoOwner, repoName, prNum, headRef, defaultBranch.GetName())
		}
	}
	return nil
}

func main() {
	config, err := readConfig("config.yml")
	if err != nil {
		panic(err)
	}

	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	zerolog.DefaultContextLogger = &logger

	metricsRegistry := metrics.DefaultRegistry

	cc, err := githubapp.NewDefaultCachingClientCreator(
		config.Github,
		githubapp.WithClientUserAgent("pr-updater-app/1.0.0"),
		githubapp.WithClientTimeout(3*time.Second),
		githubapp.WithClientCaching(false, func() httpcache.Cache { return httpcache.NewMemoryCache() }),
		githubapp.WithClientMiddleware(
			githubapp.ClientMetrics(metricsRegistry),
		),
	)
	if err != nil {
		panic(err)
	}

	prBranchUpdateHandler := &PRBranchUpdateHandler{
		ClientCreator: cc,
		preamble:      config.AppConfig.PullRequestPreamble,
	}

	webhookHandler := githubapp.NewDefaultEventDispatcher(config.Github, prBranchUpdateHandler)

	http.Handle(githubapp.DefaultWebhookRoute, webhookHandler)

	addr := fmt.Sprintf("%s:%d", config.Server.Address, config.Server.Port)
	logger.Info().Msgf("Starting server on %s...", addr)
	err = http.ListenAndServe(addr, nil)
	if err != nil {
		panic(err)
	}
}
