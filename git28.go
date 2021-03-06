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

var gitBinaryVersion, gitBinaryError = exec.Command("git", "--version").Output()

// git28 implements git support using git version 2.8+ binary.
type git28 struct{}

func (git28) Status(dir string) (string, error) {
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

func (git28) Branch(dir string) (string, error) {
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

// gitRevisionLength is the length of a git revision hash.
const gitRevisionLength = 40

func (git28) LocalRevision(dir string, defaultBranch string) (string, error) {
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

func (git28) Stash(dir string) (string, error) {
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

func (git28) Contains(dir string, revision string, defaultBranch string) (bool, error) {
	// --format=contains is just an arbitrary constant string that we look for in the output.
	cmd := exec.Command("git", "for-each-ref", "--format=contains", "--count=1", "--contains", revision, "refs/heads/"+defaultBranch)
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err == nil:
		// If this commit is contained, the expected output is exactly "contains\n".
		return bytes.Equal(stdout, []byte("contains\n")), nil
	case err != nil && bytes.HasPrefix(stderr, []byte(fmt.Sprintf("error: no such commit %s\n", revision))):
		return false, nil // No such commit error means this commit is not contained.
	default:
		return false, err
	}
}

func (git28) RemoteContains(dir string, revision string, defaultBranch string) (bool, error) {
	// --format=contains is just an arbitrary constant string that we look for in the output.
	cmd := exec.Command("git", "for-each-ref", "--format=contains", "--count=1", "--contains", revision, "refs/remotes/origin/"+defaultBranch)
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err == nil:
		// If this commit is contained, the expected output is exactly "contains\n".
		return bytes.Equal(stdout, []byte("contains\n")), nil
	case err != nil && bytes.HasPrefix(stderr, []byte(fmt.Sprintf("error: no such commit %s\n", revision))):
		return false, nil // No such commit error means this commit is not contained.
	default:
		return false, err
	}
}

func (git28) RemoteURL(dir string) (string, error) {
	// We may be on a non-default branch with a different remote set. In order to get consistent results,
	// we must assume default remote is "origin" and explicitly specify it here. If it doesn't exist,
	// then we treat that as no remote (even if some other remote exists), because this is a simple
	// and consistent thing to do.
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err != nil && bytes.Equal(stderr, []byte("fatal: No such remote 'origin'\n")):
		return "", ErrNoRemote
	case err != nil:
		return "", err
	}
	return strings.TrimSuffix(string(stdout), "\n"), nil
}

func (g git28) RemoteBranchAndRevision(dir string) (branch string, revision string, err error) {
	cmd := exec.Command("git", "ls-remote", "--symref", "origin", "HEAD", "refs/heads/*")
	cmd.Dir = dir
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	// THINK: Should we use "-c", "credential.helper=true"?
	//        It's higher priority than GIT_ASKPASS, but
	//        maybe stops private repos from working?
	env.Set("GIT_ASKPASS", "true")                                 // `true` here is not a boolean value, but a command /bin/true that will make git think it asked for a password, and prevent potential interactive password prompts (opting to return failure exit code instead).
	env.Set("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=yes") // Default for StrictHostKeyChecking is "ask", which we don't want since this is non-interactive and we prefer to fail than block asking for user input.
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err != nil && bytes.HasPrefix(stderr, []byte("fatal: 'origin' does not appear to be a git repository\n")):
		return "", "", ErrNoRemote
	case err != nil && bytes.HasPrefix(stderr, []byte("remote: Repository not found.\n")):
		return "", "", NotFoundError{Err: fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))}
	// TODO: Consider detecting connectivity errors specifically via "fatal: unable to access " prefix:
	//
	//       	(done with wi-fi turned off)
	//       	gostatus $ git ls-remote --symref origin HEAD refs/heads/*
	//       	fatal: unable to access 'https://github.com/shurcooL/gostatus/': Could not resolve host: github.com
	case err != nil:
		return "", "", fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))
	}
	branch, revision, err = parseGit28LsRemote(stdout)
	switch {
	case err == errBranchNotFound:
		// Some git servers doesn't support --symref option of ls-remote, so we need to fall back.
		branch, err = g.remoteBranch(dir)
		if err != nil {
			return "", "", err
		}
	case err != nil:
		return "", "", err
	}
	return branch, revision, nil
}

// remoteBranch is still needed to reliably get remote default branch
// when git server doesn't support --symref option of ls-remote.
func (git28) remoteBranch(dir string) (string, error) {
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

func (git28) CachedRemoteDefaultBranch() (string, error) {
	// TODO: Apply more effort to actually get a cached remote default branch.
	//       For now, just fall back to "master", but we can do better than that.
	return "", fmt.Errorf("not yet implemented for git, fall back to NoRemoteDefaultBranch")
}

func (git28) NoRemoteDefaultBranch() string {
	return "master"
}

type remoteGit28 struct{}

func (remoteGit28) RemoteBranchAndRevision(remoteURL string) (branch string, revision string, err error) {
	cmd := exec.Command("git", "ls-remote", "--symref", remoteURL, "HEAD", "refs/heads/*")
	env := osutil.Environ(os.Environ())
	env.Set("LANG", "en_US.UTF-8")
	env.Set("GIT_ASKPASS", "true")                                 // `true` here is not a boolean value, but a command /bin/true that will make git think it asked for a password, and prevent potential interactive password prompts (opting to return failure exit code instead).
	env.Set("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=yes") // Default for StrictHostKeyChecking is "ask", which we don't want since this is non-interactive and we prefer to fail than block asking for user input.
	cmd.Env = env

	stdout, stderr, err := dividedOutput(cmd)
	switch {
	case err != nil && bytes.HasPrefix(stderr, []byte("remote: Repository not found.\n")):
		return "", "", NotFoundError{Err: fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))}
	// TODO: Consider detecting connectivity errors specifically via "fatal: unable to access " prefix.
	case err != nil:
		return "", "", fmt.Errorf("%v: %s", err, strings.TrimSuffix(string(stderr), "\n"))
	}
	branch, revision, err = parseGit28LsRemote(stdout)
	switch {
	case err == errBranchNotFound:
		// Some git servers doesn't support --symref option of ls-remote, so we need to fall back.
		// Use guessBranch for now, because it's the best option I can think of at this time.
		branch, err = guessBranch(stdout, revision)
		if err != nil {
			return "", "", err
		}
	case err != nil:
		return "", "", err
	}
	return branch, revision, nil
}

// parseGit28LsRemote parses the branch and revision from output of
// ls-remote --symref. It returns errBranchNotFound if HEAD branch is not found.
// This can happen if git server doesn't support --symref option.
func parseGit28LsRemote(out []byte) (branch string, revision string, err error) {
	if len(out) == 0 {
		return "", "", errors.New("empty ls-remote output")
	}
	lines := strings.Split(string(out[:len(out)-1]), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if parts[1] != "HEAD" {
			continue
		}
		if strings.HasPrefix(parts[0], "ref: refs/heads/") {
			// "ref: refs/heads/master	HEAD".
			branch = parts[0][len("ref: refs/heads/"):]
		} else {
			// "7cafcd837844e784b526369c9bce262804aebc60	HEAD".
			revision = parts[0]
		}

		if branch != "" && revision != "" {
			return branch, revision, nil
		}
	}
	switch {
	case branch == "" && revision != "":
		return "", revision, errBranchNotFound
	default:
		return "", "", errors.New("HEAD branch or revision not found in ls-remote output")
	}
}

// errBranchNotFound is returned when parseGit28LsRemote can't find HEAD branch
// in ls-remote --symref output. This can happen for git servers that don't support it.
var errBranchNotFound = errors.New("HEAD branch not found in ls-remote output")

// guessBranch makes a best effort guess of determining HEAD branch
// from output of ls-remote where --symref option wasn't supported by git server.
// There doesn't seem to be a fully reliable way of determining it (I'm happy to be proven wrong).
//
// It's used by remoteGit28.RemoteBranchAndRevision as a fallback,
// and it does same guessing logic as parseGit17LsRemote.
func guessBranch(out []byte, revision string) (branch string, err error) {
	if len(out) == 0 {
		return "", errors.New("empty ls-remote output")
	}
	lines := strings.Split(string(out[:len(out)-1]), "\n")
	for _, line := range lines {
		// E.g., "7cafcd837844e784b526369c9bce262804aebc60	refs/heads/main".
		revRef := strings.SplitN(line, "\t", 2)
		rev, ref := revRef[0], revRef[1]
		if rev != revision || ref == "HEAD" {
			continue
		}
		// HACK: There may be more than one branch that matches; prefer "master" over all
		//       others, but otherwise no choice but to pick a random one, since there does
		//       not seem to be a way of finding it exactly (I'm happy to be proven wrong though).
		//       Unfortunately some git servers still don't support --symref option.
		if branch != "master" {
			branch = ref[len("refs/heads/"):]
		}
	}
	if branch == "" {
		return "", errBranchNotFound
	}
	return branch, nil
}
