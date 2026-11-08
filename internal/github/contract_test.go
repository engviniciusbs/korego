package github_test

import (
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/github"
)

// Garante em compilação que Service implementa deploy.TokenSource.
var _ deploy.TokenSource = (*github.Service)(nil)
