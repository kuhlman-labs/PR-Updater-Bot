# Pull Updater Bot

This is a simple GitHub App that will attempt to update the head of a pull request branch as commits are pushed to the default branch of the repository. This is useful for keeping pull requests up to date with the default branch.

## Usage

### Install Prerequisites

Make sure you have Go installed and configured. You can find instructions [here](https://golang.org/doc/install).

#### Clone the Repository and Install Dependencies

Clone the repository to your local machine. Install the dependencies by running `go mod tidy` in the root of the `pull-request-updater` foler.

### Create a GitHub App

Create a GitHub App in your Organization or User settings. You can find instructions [here](https://developer.github.com/apps/building-github-apps/creating-a-github-app/).

#### Permissions

The app will need the following permissions:

| Permission | Access |
| ---------- | ------ |
| Pull requests | Read & Write |
| Content | Read & Write |

Subscribe to the following events:

* `Push`

#### Generate a Private Key

In your GitHub App settings, generate a private key. You can find instructions [here](https://developer.github.com/apps/building-github-apps/creating-a-github-app/#generating-a-private-key).

#### Add a Webhook

In your GitHub App settings, add a webhook and webhook secret. You can find instructions [here](https://developer.github.com/apps/building-github-apps/creating-a-github-app/#creating-a-webhook).

Example webhook URL: `http://<ip>:<port>/api/github/hook`

### Update the Configuration File

In the `config.yml` file, update the `app_id` , `wehook secret` and `private_key` fields with the values from your GitHub App. Update the URL field with the URL of your GitHub Enterprise instance.

For the `app_configuration` field, you can specify the `pull_request_preamble` which will be added to the PR comment when the PR is updated. You can also specify the `pull_request_labels` which will filter the PRs that the app will update. If you do not specify any labels, the app will update all Open PRs.


### Run the App

Start the app by running `go run pull-updater.go`. You can also build the app by running `go build` and then running the executable.
