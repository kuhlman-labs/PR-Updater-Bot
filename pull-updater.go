package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v53/github"
	"github.com/gregjones/httpcache"
	"github.com/palantir/go-githubapp/githubapp"
	"github.com/pkg/errors"
	"github.com/rcrowley/go-metrics"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v2"
)

// Struct to hold the GitHub App client and configuration
type PRBranchUpdateHandler struct {
	githubapp.ClientCreator
	preamble string
	labels   []string
}

// Struct to hold the server and app configuration
type Config struct {
	Server    HTTPConfig          `yaml:"server"`
	Github    githubapp.Config    `yaml:"github"`
	AppConfig MyApplicationConfig `yaml:"app_configuration"`
}

// Struct to hold the application configuration
type MyApplicationConfig struct {
	PullRequestPreamble string   `yaml:"pull_request_preamble"`
	PullRequestLabels   []string `yaml:"pull_request_labels"`
}

// Struct to hold the HTTP server configuration
type HTTPConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

// Read the server configuration
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

// Return the event types that the handler will handle
func (h *PRBranchUpdateHandler) Handles() []string {
	return []string{"push"}
}

// Check if the pull request has the required labels from the configuration
func hasAllLabels(labels []string, prLabels []*github.Label) bool {
	for _, label := range labels {
		if !contains(prLabels, label) {
			return false
		}
	}
	return true
}

func contains(prLabels []*github.Label, label string) bool {
	for _, prLabel := range prLabels {
		prLabel := prLabel.GetName()
		prLabel = strings.ToLower(prLabel)
		if prLabel == label {
			return true
		}
	}
	return false
}

// This handler is called when the server recives a webhook push event.
// The handler will check if the push was to the default branch and if so
// check if there are any open pull requests that are approved to merge and
// are behind the default branch. If so, the pull request will be updated
// to the latest default branch commit.
func (h *PRBranchUpdateHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	// Create a new logger
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Get the push event payload
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

	// Get all open pull requests
	logger.Info().Msgf("Getting all open pull requests for %s/%s\n", repoOwner, repoName)
	pullRequests, _, err := client.PullRequests.List(ctx, repoOwner, repoName, &github.PullRequestListOptions{
		State: "open",
	})
	if err != nil {
		return err
	}
	logger.Info().Msgf("Found %d open pull requests\n", len(pullRequests))

	// Iterate over all open pull requests
	for _, pr := range pullRequests {
		// Get the pull request information
		prNum := pr.GetNumber()
		headRef := pr.GetHead().GetRef()
		baseRef := pr.GetBase().GetRef()
		prLabels := pr.Labels

		// Check if the pull request has the correct labels
		hasLabels := true
		if len(h.labels) > 0 {
			logger.Info().Msgf("Checking if pull request %s/%s#%d has the correct labels\n", repoOwner, repoName, prNum)
			hasLabels = hasAllLabels(h.labels, prLabels)
			if hasLabels {
				logger.Info().Msgf("Pull request %s/%s#%d has the correct labels\n", repoOwner, repoName, prNum)
			} else {
				logger.Info().Msgf("Pull request %s/%s#%d does not have the correct labels\n", repoOwner, repoName, prNum)
				continue
			}
		}

		// Compare the pull request head to the default branch
		commitComparison, _, _ := client.Repositories.CompareCommits(ctx, repoOwner, repoName, baseRef, headRef, nil)

		logger.Info().Msgf("Pull request %s/%s#%d is behind default branch %s by %d commits\n", repoOwner, repoName, prNum, repoDefaultBranch, commitComparison.GetBehindBy())

		// Check if the pull request is behind the default branch
		if commitComparison.GetBehindBy() >= 1 {
			logger.Info().Msgf("Pull request %s/%s#%d is behind default branch %s\n", repoOwner, repoName, prNum, repoDefaultBranch)
			// Update the pull request
			updateResponse, _, err := client.PullRequests.UpdateBranch(ctx, repoOwner, repoName, prNum, nil)
			if err != nil {
				// Check if the error is due to the job being scheduled on GitHub side
				if err.Error() == "job scheduled on GitHub side; try again later" {
					logger.Info().Msgf("Job scheduled on GitHub side\n")

					// Comment on the pull request
					msg := fmt.Sprintf("%s\n\n%s", h.preamble, updateResponse.GetMessage())
					prComment := github.IssueComment{
						Body: &msg,
					}
					logger.Info().Msgf("Commenting on pull request %s/%s#%d\n", repoOwner, repoName, prNum)
					if _, _, err := client.Issues.CreateComment(ctx, repoOwner, repoName, prNum, &prComment); err != nil {
						return err
					}
				} else {
					// Comment on the pull request that the update failed
					msg := fmt.Sprintf("Failed to update pull request. Error: %s", err.Error())
					prComment := github.IssueComment{
						Body: &msg,
					}
					logger.Info().Msgf("Commenting on pull request %s/%s#%d\n", repoOwner, repoName, prNum)
					if _, _, err := client.Issues.CreateComment(ctx, repoOwner, repoName, prNum, &prComment); err != nil {
						return err
					}
				}
			}
			logger.Info().Msgf("Updated pull request %s/%s#%d. Message: %s\n", repoOwner, repoName, prNum, updateResponse.GetMessage())
		} else {
			logger.Info().Msgf("Pull request %s/%s#%d on branch %s is up to date with default branch %s\n", repoOwner, repoName, prNum, headRef, repoDefaultBranch)
		}
	}
	return nil
}

func main() {
	// Read the configuration file
	config, err := readConfig("config.yml")
	if err != nil {
		panic(err)
	}

	// Create the logger
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	zerolog.DefaultContextLogger = &logger

	// Create the metrics registry
	metricsRegistry := metrics.DefaultRegistry

	// Create the GitHub App client creator
	cc, err := githubapp.NewDefaultCachingClientCreator(
		config.Github,
		githubapp.WithClientUserAgent("pr-updater-app/1.0.0"),
		githubapp.WithClientTimeout(3*time.Second),
		githubapp.WithClientCaching(true, func() httpcache.Cache { return httpcache.NewMemoryCache() }),
		githubapp.WithClientMiddleware(
			githubapp.ClientMetrics(metricsRegistry),
		),
	)
	if err != nil {
		panic(err)
	}

	// Create the HTTP handler
	prBranchUpdateHandler := &PRBranchUpdateHandler{
		ClientCreator: cc,
		preamble:      config.AppConfig.PullRequestPreamble,
		labels:        config.AppConfig.PullRequestLabels,
	}
	webhookHandler := githubapp.NewDefaultEventDispatcher(config.Github, prBranchUpdateHandler)

	// Create the HTTP server
	http.Handle(githubapp.DefaultWebhookRoute, webhookHandler)
	addr := fmt.Sprintf("%s:%d", config.Server.Address, config.Server.Port)
	logger.Info().Msgf("Starting server on %s...", addr)

	// Start the HTTP server
	err = http.ListenAndServe(addr, nil)
	if err != nil {
		panic(err)
	}
}
