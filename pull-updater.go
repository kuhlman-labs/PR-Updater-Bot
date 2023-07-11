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
	return []string{"pull_request"}
}

func (h *PRBranchUpdateHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	// Parse the pull request event
	var event github.PullRequestEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return errors.Wrap(err, "failed to parse pull request event payload")
	}

	// Get the pull request information
	repo := event.GetPullRequest().GetBase().GetRepo()
	prNum := event.GetPullRequest().GetNumber()
	installationID := githubapp.GetInstallationIDFromEvent(&event)
	repoOwner := event.Repo.GetOwner().GetLogin()
	repoName := event.Repo.GetName()
	author := event.GetPullRequest().GetUser().GetLogin()
	headRef := event.GetPullRequest().GetHead().GetRef()
	headSha := event.GetPullRequest().GetHead().GetSHA()
	baseRef := event.GetPullRequest().GetBase().GetRef()

	// Prepare the context
	ctx, logger := githubapp.PreparePRContext(ctx, installationID, repo, event.GetNumber())

	// Check if the pull request was opened
	if event.GetAction() == "opened" {
		return nil
	}

	// Get the installation client
	client, err := h.NewInstallationClient(installationID)
	if err != nil {
		return err
	}

	// Get latest commit on base branch
	baseRepo, _, err := client.Repositories.GetBranch(ctx, repoOwner, repoName, baseRef, true)
	if err != nil {
		return err
	}
	baseSha := baseRepo.GetCommit().GetSHA()

	// Compare the pull request head to the base branch
	commitComparison, _, _ := client.Repositories.CompareCommits(ctx, repoOwner, repoName, baseSha, headSha, nil)

	// Check if the pull request is behind the base branch
	if commitComparison.GetBehindBy() >= 1 {
		logger.Debug().Msgf("Pull request %s/%s#%d is behind base branch %s", repoOwner, repoName, prNum, baseRef)
		logger.Debug().Msgf("Updating pull request %s/%s#%d by %s", repoOwner, repoName, prNum, author)

		// Update the pull request
		updateResponse, _, err := client.PullRequests.UpdateBranch(ctx, repoOwner, repoName, prNum, nil)
		if err != nil {
			// Check if the error is due to the job being scheduled on GitHub side
			if err.Error() == "job scheduled on GitHub side; try again later" {
				logger.Debug().Msgf("Job scheduled on GitHub side")

				// Comment on the pull request
				msg := fmt.Sprintf("%s\n\n%s", h.preamble, updateResponse.GetMessage())
				prComment := github.IssueComment{
					Body: &msg,
				}
				logger.Debug().Msgf("Commenting on pull request %s/%s#%d by %s", repoOwner, repoName, prNum, author)

				if _, _, err := client.Issues.CreateComment(ctx, repoOwner, repoName, prNum, &prComment); err != nil {
					logger.Error().Err(err).Msg("Failed to comment on pull request")
				}

				return nil

			} else {
				logger.Error().Err(err).Msgf("Failed to update pull request. Error: %s", err.Error())
				return err
			}
		}

	} else {
		logger.Debug().Msgf("Pull request %s/%s#%d on branch %s is up to date with base branch %s", repoOwner, repoName, prNum, headRef, baseRef)
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
