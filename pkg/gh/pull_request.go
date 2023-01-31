package gh

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/go-version"

	"github.com/google/go-github/v48/github"

	"github.com/pkg/errors"
)

type NewPullRequest struct {
	BasePullRequest
	Title string
	Body  string
}

type GetPullRequest struct {
	BasePullRequest
	PackageName string
	Version     string
}

// OpenPullRequest opens a pull request on GitHub
func (o *GitOptions) OpenPullRequest(pr *NewPullRequest) (string, error) {
	// if our new version is more recent that the existing PR close it and create a new one, otherwise skip

	// If the current request has already exhausted the configured number of PR retries, short-circuit
	if pr.Retries > o.MaxPullRequestRetries {
		return "", fmt.Errorf("failed max number of retries, tried %d max %d", pr.Retries, o.MaxPullRequestRetries)
	}

	// Configure pull request options that the GitHub client accepts when making calls to open new pull requests
	newPR := &github.NewPullRequest{
		Title: github.String(pr.Title),
		Head:  github.String(pr.Branch),
		Base:  github.String(pr.PullRequestBaseBranch),
		Body:  github.String(pr.Body),
	}

	// make a pull request
	githubPR, resp, err := o.GithubClient.PullRequests.Create(context.Background(), pr.Owner, pr.RepoName, newPR)

	githubErr := github.CheckResponse(resp.Response)

	if githubErr != nil {
		isRateLimited, delay := o.checkRateLimiting(githubErr)
		if isRateLimited {
			pr.Retries++
			o.wait(delay)

			// retry opening a pull request
			return o.OpenPullRequest(pr)
		}
	}

	if err != nil {
		return "", errors.Wrapf(err, "failed opening pull request")
	}

	return githubPR.GetHTMLURL(), nil
}

// CheckExistingPullRequests if an existing PR is open with the same version skip, if it's an older version close the PR and we'll create a new one
func (o *GitOptions) CheckExistingPullRequests(pr *GetPullRequest) (string, error) {
	// check if there's an existing PR open for the same package
	openPullRequests, resp, err := o.GithubClient.PullRequests.List(context.Background(), pr.Owner, pr.RepoName, &github.PullRequestListOptions{State: "open"})

	githubErr := github.CheckResponse(resp.Response)

	if githubErr != nil {
		isRateLimited, delay := o.checkRateLimiting(githubErr)

		if isRateLimited {
			pr.Retries++
			o.wait(delay)

			// retry opening a pull request
			return o.CheckExistingPullRequests(pr)
		}
	}

	if err != nil {
		return "", errors.Wrapf(err, "failed listing pull requests")
	}

	for _, openPr := range openPullRequests {
		// if we already have a PR for the same version return
		if strings.HasPrefix(*openPr.Title, fmt.Sprintf("%s/%s", pr.PackageName, pr.Version)) {
			return openPr.GetHTMLURL(), nil
		}

		prTitle := *openPr.Title
		// if we have a pull request for the package but it's for an old version close it
		isOld := o.isPullRequestOldVersion(pr.PackageName, pr.Version, prTitle)

		if isOld {
			o.Logger.Printf("closing old pull request %s as we have a newer version %s", openPr.GetHTMLURL(), pr.Version)
			err = o.closePullRequest(pr, openPr)
			if err != nil {
				o.Logger.Printf("failed closing old pull request %s.  Error: %s", openPr.GetHTMLURL(), err.Error())
			}
		}
	}

	return "", nil
}

func (o *GitOptions) closePullRequest(pr *GetPullRequest, openPr *github.PullRequest) error {
	closed := "closed"
	openPr.State = &closed

	_, resp, err := o.GithubClient.PullRequests.Edit(context.Background(), pr.Owner, pr.RepoName, *openPr.Number, openPr)
	githubErr := github.CheckResponse(resp.Response)

	if githubErr != nil {
		isRateLimited, delay := o.checkRateLimiting(githubErr)

		if isRateLimited {
			// If this request has been seen before, increment its retries count, taking into account previous iterations
			pr.Retries++
			o.wait(delay)

			// retry opening a pull request
			return o.closePullRequest(pr, openPr)
		}
	}
	return err
}

// a matching pull request will have a title in the form of "package_name/v1.2.3 package update"
func (o *GitOptions) isPullRequestOldVersion(packageName, packageVersion, prTitle string) bool {
	if strings.HasPrefix(prTitle, fmt.Sprintf("%s/", packageName)) {
		parts := strings.SplitAfter(prTitle, fmt.Sprintf("%s/", packageName))
		if len(parts) != 2 {
			return false
		}

		// try and get a version after the package name.
		versionParts := strings.SplitAfter(parts[1], " ")
		if len(versionParts) == 0 {
			return false
		}

		currentVersion := strings.TrimSpace(versionParts[0])

		// get the version from the existing pull request title
		currentVersionSemver, err := version.NewVersion(currentVersion)
		if err != nil {
			o.Logger.Printf("cannot get new version from current version %s. Error %s", currentVersion, err.Error())
			return false
		}

		// get a comparable version format for our new package version
		latestVersionSemver, err := version.NewVersion(packageVersion)
		if err != nil {
			o.Logger.Printf("cannot get new version from package version %s. Error %s", packageVersion, err.Error())
			return false
		}

		// compare if the existing open pull request has an older version, if so close it and continue to create a new onw
		if currentVersionSemver.LessThan(latestVersionSemver) {
			return true
		}
	}
	return false
}
