// Package repoid handles repository_id, the UUID under which the backend
// knows a repository. It is set by hand for now and kept in the
// repository's local Git configuration so that the post-commit hook can
// read it.
package repoid

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
)

// ConfigKey is the local Git configuration key holding the repository ID.
const ConfigKey = "commitcoach.repositoryId"

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ErrNotSet means no repository ID was given and none is saved.
var ErrNotSet = errors.New("repository_id is not set: save it with \"commitcoach init --repository-id <UUID>\" " +
	"(or git config --local " + ConfigKey + " <UUID>), or pass --repository-id to export")

// Normalize checks that s is a UUID in the 8-4-4-4-12 hexadecimal form and
// returns it in lowercase. The nil UUID is rejected: it is a placeholder,
// never an ID the backend hands out.
func Normalize(s string) (string, error) {
	id := strings.ToLower(s)
	if !uuidRE.MatchString(id) {
		return "", fmt.Errorf("repository ID %q is not a UUID (expected the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)", s)
	}
	if id == "00000000-0000-0000-0000-000000000000" {
		return "", fmt.Errorf("repository ID %q is the nil UUID, not a registered repository", s)
	}
	return id, nil
}

// Load returns the repository ID saved in the repository's local
// configuration. Settings in other scopes (global, system) are ignored,
// because an ID belongs to one repository.
func Load(ctx context.Context, repo *gitrepo.Repo) (string, error) {
	values, err := repo.ConfigValues(ctx, ConfigKey)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", ConfigKey, err)
	}
	saved := ""
	for _, v := range values {
		if v.Scope == "local" {
			saved = v.Value // the last one wins, as in git
		}
	}
	if saved == "" {
		return "", ErrNotSet
	}
	id, err := Normalize(saved)
	if err != nil {
		return "", fmt.Errorf("%s in the local git config: %w", ConfigKey, err)
	}
	return id, nil
}

// Resolve returns flag when it is not empty (without saving it), and the
// saved ID otherwise.
func Resolve(ctx context.Context, repo *gitrepo.Repo, flag string) (string, error) {
	if flag != "" {
		return Normalize(flag)
	}
	return Load(ctx, repo)
}

// Save writes id to the repository's local configuration.
func Save(ctx context.Context, repo *gitrepo.Repo, id string) error {
	id, err := Normalize(id)
	if err != nil {
		return err
	}
	if err := repo.SetLocalConfig(ctx, ConfigKey, id); err != nil {
		return fmt.Errorf("saving %s: %w", ConfigKey, err)
	}
	return nil
}
