package hostedcli

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// KindModernization is deliberately absent from hosted PlanCommand admission.
const KindModernization Kind = "local-modernization"

var modernizationPreview = regexp.MustCompile(`^[a-f0-9]{48}$`)

// PlanLocalOperator keeps domain operations separate from maintenance authority.
func PlanLocalOperator(command string) (Plan, error) {
	tokens, err := Tokenize(command)
	if err != nil {
		return Plan{}, err
	}
	if !hasPrefix(tokens, "gh", "cao", "modernization") {
		return PlanLocalMaintenance(command)
	}
	if len(tokens) < 6 || !slices.Contains([]string{"status", "preview", "execute"}, tokens[3]) ||
		tokens[4] != "--repo" || !ValidRepository(tokens[5]) || strings.Contains(tokens[5], "..") {
		return Plan{}, errors.New("modernization requires an exact operation and repository")
	}
	args := tokens[3:]
	switch args[0] {
	case "status":
		if len(args) != 3 {
			return Plan{}, errors.New("status does not accept operation arguments")
		}
	case "preview":
		if len(args) != 7 || args[3] != "--action" || args[5] != "--mode" ||
			!slices.Contains([]string{"assess", "approve-plan", "run", "verify"}, args[4]) ||
			!slices.Contains([]string{"review", "live"}, args[6]) || args[4] != "run" && args[6] != "review" {
			return Plan{}, errors.New("invalid modernization preview action or mode")
		}
	case "execute":
		if len(args) != 5 || args[3] != "--preview" || !modernizationPreview.MatchString(args[4]) {
			return Plan{}, errors.New("execution requires one exact native preview")
		}
	}
	return Plan{Kind: KindModernization, Program: "gh-cao", Args: append([]string{"modernization"}, args...), Repository: strings.ToLower(tokens[5])}, nil
}

func modernizationPlanInput(command string) bool {
	plan, err := PlanLocalOperator(command)
	return err == nil && plan.Kind == KindModernization && len(plan.Args) == 8 &&
		plan.Args[1] == "preview" && plan.Args[5] == "approve-plan"
}
