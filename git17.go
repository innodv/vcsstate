package vcsstate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/shurcooL/go/osutil"
)

// git17 implements git support using git version 1.7+ binary.
type git17 struct{}

func (git17) Status(dir string) (string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (git17) Branch(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Since rev-parse is considered porcelain and may change, need to error-check its output.
	return strings.TrimSuffix(string(out), "\n"), nil
}

func (git17) LocalRevision(dir string, defaultBranch string) (string, error) {
	cmd := exec.Command("git", "rev-parse", defaultBranch)
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if len(out) < gitRevisionLength {
		return "", fmt.Errorf("output length %v is shorter than %v", len(out), gitRevisionLength)
	}
	return string(out[:gitRevisionLength]), nil
}

func (git17) Stash(dir string) (string, error) {
	cmd := exec.Command("git", "stash", "list")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (git17) Contains(dir string, revision string, defaultBranch string) (bool, error) {
	cmd := exec.Command("git", "branch", "--contains", revision, defaultBranch)
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err == nil:
		// If this commit is contained, the expected output is exactly "* {defaultBranch}\n"
		// or "  {defaultBranch}\n" if we're on another branch,
		// where {defaultBranch} is the value of defaultBranch.
		return bytes.Equal(stdout, []byte(fmt.Sprintf("* %s\n", defaultBranch))) ||
			bytes.Equal(stdout, []byte(fmt.Sprintf("  %s\n", defaultBranch))), nil
	case err != nil && bytes.HasPrefix(stderr, []byte(fmt.Sprintf("error: no such commit %s\n", revision))):
		return false, nil // No such commit error means this commit is not contained.
	default:
		return false, err
	}
}

func (git17) RemoteContains(dir string, revision string, defaultBranch string) (bool, error) {
	cmd := exec.Command("git", "branch", "-r", "--contains", revision, "origin/"+defaultBranch)
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err == nil:
		// If this commit is contained, the expected output is exactly "  origin/{defaultBranch}\n",
		// where {defaultBranch} is the value of defaultBranch.
		return bytes.Equal(stdout, []byte(fmt.Sprintf("  origin/%s\n", defaultBranch))), nil
	case err != nil && bytes.HasPrefix(stderr, []byte(fmt.Sprintf("error: no such commit %s\n", revision))):
		return false, nil // No such commit error means this commit is not contained.
	default:
		return false, err
	}
}

func (git17) RemoteURL(dir string) (string, error) {
	// We may be on a non-default branch with a different remote set. In order to get consistent results,
	// we must assume default remote is "origin" and explicitly specify it here. If it doesn't exist,
	// then we treat that as no remote (even if some other remote exists), because this is a simple
	// and consistent thing to do.
	// TODO: Once git 2.7 becomes generally available, consider reverting back to `git remote get-url origin`.
	cmd := exec.Command("git", "remote", "-v")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	url, err := parseGit17Remote(out)
	if err != nil {
		return "", ErrNoRemote
	}
	return url, nil
}

func (g git17) RemoteBranchAndRevision(dir string) (branch string, revision string, err error) {
	cmd := exec.Command("git", "ls-remote", "origin", "HEAD", "refs/heads/*")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	env.Set("GIT_ASKPASS", "true")                                 // `true` here is not a boolean value, but a command /bin/true that will make git think it asked for a password, and prevent potential interactive password prompts (opting to return failure exit code instead).
	env.Set("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=yes") // Default for StrictHostKeyChecking is "ask", which we don't want since this is non-interactive and we prefer to fail than block asking for user input.
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err != nil && bytes.HasPrefix(stderr, []byte("fatal: 'origin' does not appear to be a git repository\n")):
		return "", "", ErrNoRemote
	case err != nil:
		return "", "", fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))
	}
	_, revision, err = parseGit17LsRemote(stdout)
	if err != nil {
		return "", "", err
	}
	branch, err = g.remoteBranch(dir)
	if err != nil {
		return "", "", err
	}
	return branch, revision, nil
}

// remoteBranch is needed to reliably get remote default branch until git 2.8 becomes commonly available.
func (git17) remoteBranch(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "show", "origin")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	env.Set("GIT_ASKPASS", "true")                                 // `true` here is not a boolean value, but a command /bin/true that will make git think it asked for a password, and prevent potential interactive password prompts (opting to return failure exit code instead).
	env.Set("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=yes") // Default for StrictHostKeyChecking is "ask", which we don't want since this is non-interactive and we prefer to fail than block asking for user input.
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))
	}
	const s = "\n  HEAD branch: "
	i := bytes.Index(stdout, []byte(s))
	if i == -1 {
		return "", errors.New("no HEAD branch")
	}
	i += len(s)
	nl := bytes.IndexByte(stdout[i:], '\n')
	if nl == -1 {
		nl = len(stdout)
	} else {
		nl += i
	}
	return string(stdout[i:nl]), nil
}

func (git17) CachedRemoteDefaultBranch() (string, error) {
	// TODO: Apply more effort to actually get a cached remote default branch.
	//       For now, just fall back to "master", but we can do better than that.
	return "", fmt.Errorf("not yet implemented for git, fall back to NoRemoteDefaultBranch")
}

func (git17) NoRemoteDefaultBranch() string {
	return "master"
}

type remoteGit17 struct{}

func (remoteGit17) RemoteBranchAndRevision(remoteURL string) (branch string, revision string, err error) {
	cmd := exec.Command("git", "ls-remote", remoteURL, "HEAD", "refs/heads/*")
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	env.Set("GIT_ASKPASS", "true")                                 // `true` here is not a boolean value, but a command /bin/true that will make git think it asked for a password, and prevent potential interactive password prompts (opting to return failure exit code instead).
	env.Set("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=yes") // Default for StrictHostKeyChecking is "ask", which we don't want since this is non-interactive and we prefer to fail than block asking for user input.
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	if err != nil {
		return "", "", fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))
	}
	return parseGit17LsRemote(stdout)
}

// parseGit17Remote parses the fetch URL for "origin" remote, if it exists.
func parseGit17Remote(out []byte) (url string, err error) {
	if len(out) == 0 {
		return "", errors.New("no origin remote")
	}
	lines := strings.Split(string(out[:len(out)-1]), "\n")
	for _, line := range lines {
		// E.g., "origin	https://github.com/shurcooL/vcsstate (fetch)".
		nameURLKind := strings.Split(line, "\t")
		name, urlKind := nameURLKind[0], nameURLKind[1]

		if name != "origin" {
			continue
		}
		if !strings.HasSuffix(urlKind, " (fetch)") {
			continue
		}
		url := urlKind[:len(urlKind)-len(" (fetch)")]
		return url, nil
	}
	return "", errors.New("no origin remote")
}

// parseGit17LsRemote parses the branch and revision from output of
// ls-remote (without --symref option).
func parseGit17LsRemote(out []byte) (branch string, revision string, err error) {
	if len(out) == 0 {
		return "", "", errors.New("empty ls-remote output")
	}
	lines := strings.Split(string(out[:len(out)-1]), "\n")
	for _, line := range lines {
		// E.g., "7cafcd837844e784b526369c9bce262804aebc60	refs/heads/main".
		revisionReference := strings.Split(line, "\t")
		rev, ref := revisionReference[0], revisionReference[1]

		// This assumes HEAD comes first, before all other references.
		if ref == "HEAD" {
			revision = rev
			continue
		}

		// HACK: There may be more than one branch that matches; prefer "master" over all
		//       others, but otherwise no choice but to pick a random one, since there does
		//       not seem to be a way of finding it exactly (I'm happy to be proven wrong though).
		//       In git 2.8, can use --symref option to fix this, but unfortunately some
		//       git servers still don't support that option.
		if rev == revision && branch != "master" {
			branch = ref[len("refs/heads/"):]
		}
	}
	if branch == "" || revision == "" {
		return "", "", errors.New("HEAD branch or revision not found in ls-remote output")
	}
	return branch, revision, nil
}
