package gitrepo

import (
	"context"
	"strings"
)

// RevList runs "git rev-list" with args and returns one object name per
// line, oldest last (git's default order). An empty result is not an error.
func (r *Repo) RevList(ctx context.Context, args ...string) ([]string, error) {
	out, err := r.run.Output(ctx, append([]string{"rev-list"}, args...)...)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
