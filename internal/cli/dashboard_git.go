package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type dashboardGitIdentity struct {
	Name  string
	Email string
}

// Configure only the command tree: remote maintenance creates its own checkout,
// so repository-local identity and credential configuration would not follow it.
func dashboardGitEnvironment(ctx context.Context, environment []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	run := func(environment []string, program string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, program, args...)
		command.Env = environment
		command.WaitDelay = time.Second
		return command.Output()
	}
	identity := dashboardGitIdentity{}
	if configuration, _ := ctx.Value(dashboardConfigurationKey{}).(*dashboardConfiguration); configuration != nil {
		snapshot, err := readDashboardConfiguration(configuration.path)
		if err != nil {
			return nil, errors.New("Git readiness: fix the local CAO configuration before publishing a pull request")
		}
		identity = snapshot.git
	}
	environment = append(environment, "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	if _, err := run(environment, "git", "--version"); err != nil {
		return nil, errors.New("Git readiness: install git and ensure it is on PATH")
	}
	if _, err := run(environment, "gh", "auth", "status", "--active", "--hostname", "github.com"); err != nil {
		return nil, errors.New("Git readiness: GitHub authentication is unavailable; run gh auth login --hostname github.com or configure GH_TOKEN")
	}
	configured := map[string]string{}
	if identity.Name == "" {
		for _, field := range []string{"name", "email"} {
			content, err := run(environment, "git", "config", "--get", "user."+field)
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					return nil, errors.New("Git readiness: unable to read Git identity configuration")
				}
			}
			configured[field] = strings.TrimSpace(string(content))
		}
	}
	value := func(key string) string {
		for index := len(environment) - 1; index >= 0; index-- {
			if value, ok := strings.CutPrefix(environment[index], key+"="); ok {
				return value
			}
		}
		return ""
	}
	resolved := map[string]string{}
	missing := false
	for _, role := range []string{"AUTHOR", "COMMITTER"} {
		for _, field := range []string{"NAME", "EMAIL"} {
			key := "GIT_" + role + "_" + field
			entry := identity.Name
			if field == "EMAIL" {
				entry = identity.Email
			}
			if entry == "" {
				entry = value(key)
			}
			if entry == "" {
				entry = configured[strings.ToLower(field)]
			}
			resolved[key] = entry
			missing = missing || entry == ""
		}
	}
	if missing {
		content, err := run(environment, "gh", "api", "--hostname", "github.com", "user")
		var user struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err != nil || json.Unmarshal(content, &user) != nil || user.ID <= 0 || !nativeLogin.MatchString(user.Login) {
			return nil, errors.New("Git readiness: no commit identity is configured and the signed-in GitHub user could not be resolved; set git.name and git.email in cao.json")
		}
		if user.Name == "" {
			user.Name = user.Login
		}
		if user.Email == "" {
			user.Email = fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, user.Login)
		}
		for key, entry := range resolved {
			if entry == "" {
				resolved[key] = user.Name
				if strings.HasSuffix(key, "_EMAIL") {
					resolved[key] = user.Email
				}
			}
		}
	}
	for _, role := range []string{"AUTHOR", "COMMITTER"} {
		name, email := resolved["GIT_"+role+"_NAME"], resolved["GIT_"+role+"_EMAIL"]
		if !nativeGitName.MatchString(name) || !nativeGitEmail.MatchString(email) {
			return nil, errors.New("Git readiness: invalid commit identity; set git.name and git.email in cao.json")
		}
		environment = append(environment, "GIT_"+role+"_NAME="+name, "GIT_"+role+"_EMAIL="+email)
	}
	count := 0
	if text := value("GIT_CONFIG_COUNT"); text != "" {
		var err error
		count, err = strconv.Atoi(text)
		if err != nil || count < 0 || count > 1024 {
			return nil, errors.New("Git readiness: invalid GIT_CONFIG_COUNT")
		}
	}
	settings := [][2]string{
		{"credential.https://github.com.helper", ""},
		{"credential.https://github.com.helper", "!gh auth git-credential"},
		{"url.https://github.com/.insteadOf", "git@github.com:"},
		{"url.https://github.com/.insteadOf", "ssh://git@github.com/"},
	}
	for index, setting := range settings {
		environment = append(environment,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", count+index, setting[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count+index, setting[1]))
	}
	environment = append(environment, fmt.Sprintf("GIT_CONFIG_COUNT=%d", count+len(settings)))
	for _, role := range []string{"AUTHOR", "COMMITTER"} {
		if _, err := run(environment, "git", "var", "GIT_"+role+"_IDENT"); err != nil {
			return nil, errors.New("Git readiness: Git rejected the commit identity or configuration; fix Git configuration or set git.name and git.email in cao.json")
		}
	}
	return environment, nil
}
