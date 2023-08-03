package main

import (
	"testing"

	"github.com/google/go-github/v53/github"
	"github.com/palantir/go-githubapp/githubapp"
)

func TestPRBranchUpdateHandler(t *testing.T) {

	pushEvent := &github.PushEvent{
		Head:    github.String("feature-branch"),
		Ref:     github.String("refs/heads/feature-branch"),
		BaseRef: github.String("refs/heads/main"),
		Repo: &github.PushEventRepository{
			Name: github.String("test-repo"),
			Owner: &github.User{
				Login: github.String("test-user"),
			},
			DefaultBranch: github.String("main"),
		},
		Installation: &github.Installation{
			ID: github.Int64(123456),
		},
	}

	repo := pushEvent.GetRepo()
	repoOwner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()
	repoDefaultBranch := repo.GetDefaultBranch()
	if pushEvent.GetRef() != "refs/heads/feature-branch" {
		t.Errorf("Expected %s, got %s", "refs/heads/feature-branch", pushEvent.GetRef())
	}

	if repoOwner != "test-user" {
		t.Errorf("Expected %s, got %s", "test-user", repoOwner)
	}

	if repoName != "test-repo" {
		t.Errorf("Expected %s, got %s", "test-repo", repoName)
	}

	if repoDefaultBranch != "main" {
		t.Errorf("Expected %s, got %s", "main", repoDefaultBranch)
	}

	installationID := githubapp.GetInstallationIDFromEvent(pushEvent)
	if installationID != 123456 {
		t.Errorf("Expected %d, got %d", 123456, installationID)
	}

	pullRequest := &github.PullRequest{
		Number: github.Int(1),
		Head: &github.PullRequestBranch{
			Ref: github.String("feature-branch"),
		},
		Base: &github.PullRequestBranch{
			Ref: github.String("main"),
		},
		Labels: []*github.Label{
			{
				Name: github.String("Approved to Merge"),
			},
			{
				Name: github.String("Ready to Merge"),
			},
		},
	}

	prNum := pullRequest.GetNumber()
	headRef := pullRequest.GetHead().GetRef()
	baseRef := pullRequest.GetBase().GetRef()
	labels := pullRequest.Labels
	if prNum != 1 {
		t.Errorf("Expected %d, got %d", 1, prNum)
	}

	if headRef != "feature-branch" {
		t.Errorf("Expected %s, got %s", "feature-branch", headRef)
	}

	if baseRef != "main" {
		t.Errorf("Expected %s, got %s", "main", baseRef)
	}

	if len(labels) != 2 {
		t.Errorf("Expected %d, got %d", 2, len(labels))
	}

	commitComparison := &github.CommitsComparison{
		AheadBy:  github.Int(1),
		BehindBy: github.Int(1),
	}
	aheadBy := commitComparison.GetAheadBy()
	behindBy := commitComparison.GetBehindBy()
	if aheadBy != 1 {
		t.Errorf("Expected %d, got %d", 1, aheadBy)
	}
	if behindBy != 1 {
		t.Errorf("Expected %d, got %d", 0, behindBy)
	}
}

func TestHasAllLabels(t *testing.T) {

	prLabels := []*github.Label{
		{
			Name: github.String("Approved to Merge"),
		},
		{
			Name: github.String("Ready to Merge"),
		},
		{
			Name: github.String("Needs Review"),
		},
	}

	configLabels := []string{"Approved to Merge", "Ready to Merge"}

	doesHaveLabels := hasAllLabels(configLabels, prLabels)

	if doesHaveLabels != true {
		t.Errorf("Expected %t, got %t", true, doesHaveLabels)
	}

	prLabels = []*github.Label{
		{
			Name: github.String("Approved to Merge"),
		},
	}

	configLabels = []string{"Approved to Merge", "Ready to Merge"}

	doesNotHaveLabels := hasAllLabels(configLabels, prLabels)

	if doesNotHaveLabels != false {
		t.Errorf("Expected %t, got %t", false, doesNotHaveLabels)
	}

}
