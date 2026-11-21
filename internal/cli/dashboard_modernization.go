package cli

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

func dashboardModernizationArgs(args, repositories []string) error {
	if len(args) < 4 || args[0] != "modernization" || args[2] != "--repo" ||
		!repositoryPattern.MatchString(args[3]) || strings.Contains(args[3], "..") ||
		!nativeRepositoryOperationsAllowed(args[3]) || !containsRepository(repositories, args[3]) {
		return errors.New("modernization requires the exact selected control repository")
	}
	switch args[1] {
	case "status":
		if len(args) == 4 {
			return nil
		}
	case "preview":
		if len(args) == 8 && args[4] == "--action" && args[6] == "--mode" &&
			slices.Contains([]string{"assess", "approve-plan", "run", "verify"}, args[5]) &&
			slices.Contains([]string{"review", "live"}, args[7]) && (args[5] == "run" || args[7] == "review") {
			return nil
		}
	case "execute":
		if len(args) == 6 && args[4] == "--preview" && modernizationPreviewID.MatchString(args[5]) {
			return nil
		}
	}
	return errors.New("unsupported modernization domain command")
}

func dashboardModernizationInput(args []string, input *string) bool {
	approval := len(args) == 8 && args[1] == "preview" && args[5] == "approve-plan"
	if !approval {
		return input == nil
	}
	if input == nil || utf8.RuneCountInString(*input) > 100000 {
		return false
	}
	var plan map[string]any
	return json.Unmarshal([]byte(*input), &plan) == nil && plan != nil
}
