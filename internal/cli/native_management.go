package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var nativeVersionCollators = sync.Pool{New: func() any {
	return collate.New(language.English, collate.Numeric)
}}

func nativeRepositoryAdd(cmd *cobra.Command, repository string, gh ghExecutor) error {
	if len(repository) > 200 || !nativeSetupRepositoryPattern.MatchString(repository) {
		return errors.New("repos add requires OWNER/REPO with optional * wildcards")
	}
	policy, err := nativeReadValidatedPolicy()
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is required for repository enrollment", policyPath)
	}
	if err != nil {
		return err
	}
	control, _ := policy["control-plane"].(map[string]any)
	scope, _ := control["scope"].(map[string]any)
	repositories, repositoriesOK := scope["allowed-repositories"].([]any)
	owners, ownersOK := scope["allowed-owners"].([]any)
	if !repositoriesOK || !ownersOK {
		return nativeErrorf("Repository enrollment requires explicit allowed-owners and allowed-repositories policy; broad discovery scope is not rewritten.")
	}
	requested := []string{repository}
	if strings.Contains(repository, "*") {
		matches, err := nativeSetupExpand(cmd, gh, nativeSetupRepository{}, repository)
		if err != nil {
			return err
		}
		if len(matches) > 100 {
			return errors.New("repository pattern matched more than 100 repositories; use a narrower pattern")
		}
		requested = nil
		for _, match := range matches {
			requested = append(requested, match.Repository)
		}
	}
	canonicals := make([]string, 0, len(requested))
	for _, target := range requested {
		stdout, stderr, err := gh(cmd.Context(), "repo", "view", target, "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		if err != nil {
			return commandError("Repository access could not be verified", stderr, err)
		}
		canonical := strings.TrimSpace(stdout)
		if !repositoryPattern.MatchString(canonical) || !strings.EqualFold(canonical, target) {
			return nativeErrorf("Repository lookup did not resolve the exact requested repository.")
		}
		canonicals = append(canonicals, canonical)
	}
	alreadyEnrolled := true
	message := "Repository is already enrolled; existing policy was preserved."
	for _, canonical := range canonicals {
		enrolled := false
		for _, value := range repositories {
			enrolled = enrolled || strings.EqualFold(value.(string), canonical)
		}
		if enrolled {
			continue
		}
		alreadyEnrolled = false
		repositories = append(repositories, canonical)
		scope["allowed-repositories"] = repositories
		owner := strings.SplitN(canonical, "/", 2)[0]
		found := false
		for _, value := range owners {
			found = found || strings.EqualFold(value.(string), owner)
		}
		if !found {
			owners = append(owners, owner)
			scope["allowed-owners"] = owners
		}
		campaigns, _ := control["campaigns"].(map[string]any)
		for _, value := range campaigns {
			campaign := value.(map[string]any)
			targets, _ := campaign["targets"].(map[string]any)
			if targets == nil {
				targets = map[string]any{}
				campaign["targets"] = targets
			}
			targets[canonical] = map[string]any{"mode": "review"}
		}
	}
	if !alreadyEnrolled {
		if err := nativeValidatePolicy(policy); err != nil {
			return err
		}
		if err := writePolicy(policy, false); err != nil {
			return err
		}
		message = "Repositories staged in review mode. Review, commit, and push the policy change before activation. Configure credential access separately; explicit Activity scope is unchanged. Wildcards resolve to exact current matches, not future repositories."
	}
	identity := repository
	if !strings.Contains(repository, "*") {
		identity = canonicals[0]
	}
	return printJSON(cmd, map[string]any{"command": "add", "repository": identity, "repositories": canonicals, "policy": policyPath, "alreadyEnrolled": alreadyEnrolled, "message": message})
}

func nativeVersion(ctx context.Context, gh ghExecutor) (string, error) {
	stdout, stderr, err := gh(ctx, "aw", "version")
	if err != nil {
		return "", commandError("Unable to determine gh-aw version", stderr, err)
	}
	version := versionPattern.FindString(stdout + "\n" + stderr)
	if version == "" {
		return "", nativeErrorf(`Unable to determine gh-aw version from "gh aw version" output`)
	}
	return version, nil
}

func nativeCompareVersions(left, right string) int {
	parse := func(value string) ([]string, string) {
		release, prerelease, _ := strings.Cut(strings.TrimPrefix(value, "v"), "-")
		return strings.Split(release, "."), prerelease
	}
	l, lp := parse(left)
	r, rp := parse(right)
	for index := 0; index < 3; index++ {
		if index >= len(l) || index >= len(r) {
			return strings.Compare(left, right)
		}
		// The production parser converts release components with Number().
		ln, _ := strconv.ParseFloat(l[index], 64)
		rn, _ := strconv.ParseFloat(r[index], 64)
		if ln < rn {
			return -1
		}
		if ln > rn {
			return 1
		}
	}
	if lp == rp {
		return 0
	}
	if lp == "" {
		return 1
	}
	if rp == "" {
		return -1
	}
	// Match localeCompare(..., "en", { numeric: true }), including case,
	// punctuation and numerically equivalent runs with leading zeroes.
	collator := nativeVersionCollators.Get().(*collate.Collator)
	defer nativeVersionCollators.Put(collator)
	return collator.CompareString(nativeNumericCollationText(lp), nativeNumericCollationText(rp))
}

// ICU limits each numeric collation element to 254 significant digits.
// NUL preserves element boundaries without adding a collation weight. A "1"
// prefix preserves numeric order and avoids x/text's zero-length zero element,
// which otherwise lets a following punctuation weight change zero's ordering.
func nativeNumericCollationText(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] < '0' || value[index] > '9' {
			result.WriteByte(value[index])
			index++
			continue
		}
		start := index
		for index < len(value) && value[index] >= '0' && value[index] <= '9' {
			index++
		}
		digits := value[start:index]
		for digits != "" {
			digits = strings.TrimLeft(digits, "0")
			if digits == "" {
				digits = "0"
			}
			size := min(len(digits), 254)
			result.WriteByte('1')
			result.WriteString(digits[:size])
			digits = digits[size:]
			if digits != "" {
				result.WriteByte(0)
			}
		}
	}
	return result.String()
}

func nativeInstallGhAw(ctx context.Context, gh ghExecutor, version string) error {
	if _, stderr, err := gh(ctx, "extension", "install", "github/gh-aw", "--pin", version, "--force"); err != nil {
		return commandError("Unable to install gh-aw "+version, stderr, err)
	}
	return nil
}

func nativeEnsureGhAw(ctx context.Context, gh ghExecutor, policy map[string]any) (map[string]any, error) {
	required, ok := policy["gh-aw-version"].(string)
	if !ok || !nativeExactVersion.MatchString(required) {
		return nil, fmt.Errorf("%s gh-aw-version must be a v-prefixed semantic version", policyPath)
	}
	current, _ := nativeVersion(ctx, gh)
	var previous any
	if current != "" {
		previous = current
	}
	updated := current == "" || nativeCompareVersions(current, required) < 0
	if updated {
		if err := nativeInstallGhAw(ctx, gh, required); err != nil {
			return nil, err
		}
		var err error
		current, err = nativeVersion(ctx, gh)
		if err != nil {
			return nil, err
		}
		if nativeCompareVersions(current, required) < 0 {
			return nil, nativeErrorf("Installed gh-aw %s is older than required %s", current, required)
		}
	}
	return map[string]any{"required": required, "previous": previous, "current": current, "updated": updated}, nil
}

func nativeUpgrade(cmd *cobra.Command, version string, gh ghExecutor) error {
	if !nativeExactVersion.MatchString(version) {
		return nativeErrorf("Invalid gh-aw version: %s", version)
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		return err
	}
	required, ok := policy["gh-aw-version"].(string)
	if !ok {
		return fmt.Errorf("%s gh-aw-version must be a v-prefixed semantic version", policyPath)
	}
	if nativeCompareVersions(version, required) < 0 {
		return fmt.Errorf("cao upgrade-gh-aw cannot downgrade gh-aw from %s to %s", required, version)
	}
	installed, _ := nativeVersion(cmd.Context(), gh)
	if installed != version {
		if err := nativeInstallGhAw(cmd.Context(), gh, version); err != nil {
			return err
		}
	}
	installed, err = nativeVersion(cmd.Context(), gh)
	if err != nil {
		return err
	}
	if installed != version {
		return nativeErrorf("Installed gh-aw version is %s, expected %s", installed, version)
	}
	if _, stderr, err := gh(cmd.Context(), "aw", "upgrade"); err != nil {
		return commandError("gh aw upgrade failed", stderr, err)
	}
	policy["gh-aw-version"] = version
	if err := nativeValidatePolicy(policy); err != nil {
		return err
	}
	if err := writePolicy(policy, false); err != nil {
		return err
	}
	return printJSON(cmd, map[string]any{"command": "upgrade-gh-aw", "policy": policyPath, "previous": required, "current": version})
}

type nativeUpdateOptions struct {
	ref, campaign string
	prereleases   bool
	options       []string
}

func nativeParseUpdate(args []string) (nativeUpdateOptions, error) {
	result := nativeUpdateOptions{options: []string{}}
	addValues := map[string]bool{"--dir": true, "-d": true, "--engine": true, "-e": true, "--stop-after": true, "--append": true, "--gh-aw-ref": true}
	values := map[string]bool{"--cool-down": true, "--repo": true, "-r": true, "--org": true, "--repos": true, "--pr-message": true}
	for key := range addValues {
		values[key] = true
	}
	positionals, hasRef, hasCampaign := false, false, false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !positionals && argument == "--" {
			positionals = true
			continue
		}
		if !positionals && (argument == "--campaign" || strings.HasPrefix(argument, "--campaign=")) {
			if hasCampaign {
				return result, errors.New("cao update accepts only one --campaign")
			}
			hasCampaign = true
			_, result.campaign, _ = strings.Cut(argument, "=")
			if argument == "--campaign" && index+1 < len(args) {
				index++
				result.campaign = args[index]
			}
			if !slugPattern.MatchString(result.campaign) {
				return result, errors.New("--campaign requires a safe campaign slug")
			}
			continue
		}
		if !positionals && strings.HasPrefix(argument, "-") {
			if argument == "--pre-releases" {
				result.prereleases = true
			} else {
				result.options = append(result.options, argument)
			}
			if values[argument] {
				index++
				if index >= len(args) || args[index] == "" || strings.HasPrefix(args[index], "-") {
					return result, fmt.Errorf("%s requires a value", argument)
				}
				result.options = append(result.options, args[index])
			}
			continue
		}
		if hasRef {
			return result, errors.New("cao update accepts only one ref")
		}
		hasRef, result.ref = true, argument
	}
	if hasRef && result.ref != "main" && result.ref != "master" && result.ref != "latest" && !regexp.MustCompile(`^[a-fA-F0-9]{7,40}$`).MatchString(result.ref) {
		return result, errors.New("cao update requires main, master, latest, or a commit SHA (7-40 hexadecimal characters)")
	}
	if hasRef {
		flags := map[string]bool{"--force": true, "-f": true, "--no-security-scanner": true, "--no-stop-after": true, "--no-gitattributes": true, "--verbose": true, "-v": true, "--banner": true}
		if result.prereleases {
			return result, nativeErrorf("Unsupported option for cao update with a ref: --pre-releases; use gh-aw add options, not release-update options")
		}
		for index := 0; index < len(result.options); index++ {
			argument := result.options[index]
			flag, inline, equals := strings.Cut(argument, "=")
			if flags[flag] && !equals {
				continue
			}
			if addValues[flag] {
				if !equals {
					index++
					if index < len(result.options) {
						inline = result.options[index]
					}
				}
				if inline != "" && !strings.HasPrefix(inline, "-") {
					continue
				}
				return result, fmt.Errorf("%s requires a value", flag)
			}
			return result, nativeErrorf("Unsupported option for cao update with a ref: %s; use gh-aw add options, not release-update options", argument)
		}
	}
	return result, nil
}

func nativeResolveUpdateRef(ctx context.Context, gh ghExecutor, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	source := ref
	switch ref {
	case "main", "master":
		stdout, stderr, err := gh(ctx, "api", "--hostname", "github.com", "/repos/"+catalogRepository, "--jq", ".default_branch")
		if err != nil {
			return "", commandError("Unable to resolve CAO default branch", stderr, err)
		}
		source = strings.TrimSpace(stdout)
		if source == "" || source == "null" || strings.ContainsAny(source, " \t\r\n") {
			return "", nativeErrorf("Unable to resolve CAO default branch: expected a branch name")
		}
	case "latest":
		stdout, stderr, err := gh(ctx, "api", "--hostname", "github.com", "/repos/"+catalogRepository+"/releases?per_page=100", "--paginate", "--jq", ".[] | select(.draft == false and .prerelease == false) | .tag_name")
		if err != nil {
			return "", commandError("Unable to resolve latest stable CAO release", stderr, err)
		}
		source = ""
		for _, tag := range strings.Fields(stdout) {
			if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(tag) && (source == "" || nativeCompareVersions(tag, source) > 0) {
				source = tag
			}
		}
		if source == "" {
			return "", nativeErrorf("Unable to resolve latest stable CAO release: expected a published vMAJOR.MINOR.PATCH release tag")
		}
	}
	stdout, stderr, err := gh(ctx, "api", "--hostname", "github.com", "/repos/"+catalogRepository+"/commits/"+url.PathEscape(source), "--jq", ".sha")
	if err != nil {
		return "", commandError("Unable to resolve CAO ref "+ref, stderr, err)
	}
	commit := strings.ToLower(strings.TrimSpace(stdout))
	if !commitPattern.MatchString(commit) || ((ref != "main" && ref != "master" && ref != "latest") && !strings.HasPrefix(commit, strings.ToLower(ref))) {
		return "", nativeErrorf("Unable to resolve CAO ref %s: expected a matching full commit SHA", ref)
	}
	return commit, nil
}

type nativePackageRecord struct {
	name, source, revision, filename string
	document                         map[string]any
}

func nativeInstalledPackages() ([]nativePackageRecord, error) {
	return nativeInstalledPackagesIn(".")
}

func nativeInstalledPackagesIn(base string) ([]nativePackageRecord, error) {
	found := map[string]nativePackageRecord{}
	for _, directory := range []string{"campaigns", "packages"} {
		root, err := repositoryPath(filepath.Join(base, ".github", "aw", directory))
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			filename, err := repositoryPath(filepath.Join(root, entry.Name()))
			if err != nil {
				return nil, err
			}
			content, err := os.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			value, err := nativeDecodeJSON(content)
			if err != nil {
				return nil, fmt.Errorf("%s contains invalid JSON: %w", filename, err)
			}
			document, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s must contain a JSON object", filename)
			}
			text := func(key string) string {
				value, _ := document[key].(string)
				return strings.TrimSpace(value)
			}
			name := text("package")
			if name == "" {
				name = text("campaign")
			}
			if name == "" {
				name, _, _ = strings.Cut(text("source"), "@")
			}
			if name != catalogRepository && !strings.HasPrefix(name, catalogRepository+"/") {
				continue
			}
			if name != catalogRepository && !slugPattern.MatchString(strings.TrimPrefix(name, catalogRepository+"/")) {
				return nil, fmt.Errorf("invalid installed CAO package identity: %s", name)
			}
			source := text("source")
			if source == "" {
				source = name
			}
			found[name] = nativePackageRecord{name, source, text("resolvedCommit"), filename, document}
		}
	}
	records := make([]nativePackageRecord, 0, len(found))
	for _, record := range found {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].name < records[j].name })
	return records, nil
}

func nativeUpdate(cmd *cobra.Command, args []string, gh ghExecutor) error {
	options, err := nativeParseUpdate(args)
	if err != nil {
		return err
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		return err
	}
	records, err := nativeInstalledPackages()
	if err != nil {
		return err
	}
	if options.campaign != "" {
		selected := []nativePackageRecord{}
		for _, record := range records {
			if nativePackageSlug(record.name) == options.campaign {
				selected = append(selected, record)
			}
		}
		if len(selected) != 1 {
			return nativeErrorf("Unknown or ambiguous installed CAO campaign: %s", options.campaign)
		}
		records = selected
	}
	if len(records) == 0 {
		return nativeErrorf("No installed gh-aw package records found under .github/aw/packages")
	}
	for _, record := range records {
		if !commitPattern.MatchString(record.revision) {
			return nativeErrorf("Installed CAO package record must contain a full resolvedCommit SHA: %s", record.revision)
		}
	}
	ghAw, err := nativeEnsureGhAw(cmd.Context(), gh, policy)
	if err != nil {
		return err
	}
	revision, err := nativeResolveUpdateRef(cmd.Context(), gh, options.ref)
	if err != nil {
		return err
	}
	updated, declarations := []string{}, []string{}
	tags := map[string]string{}
	for _, record := range records {
		var prepared *nativePreparedPackage
		if revision == "" {
			prepared, err = nativePrepareRelease(cmd.Context(), gh, record, options, tags)
			if err != nil {
				return err
			}
		}
		arguments := []string{"aw", "update", "https://github.com/" + record.name}
		if revision != "" {
			arguments = []string{"aw", "add", record.name + "@" + revision, "--force"}
			if record.name == catalogRepository && !nativeContains(options.options, "--no-security-scanner") {
				arguments = append(arguments, "--no-security-scanner")
			}
		}
		arguments = append(arguments, options.options...)
		if _, stderr, err := gh(cmd.Context(), arguments...); err != nil {
			restoreErr := nativeRestorePackage(prepared)
			return errors.Join(commandError("gh aw "+arguments[1]+" failed for "+record.name, stderr, err), restoreErr)
		}
		if revision != "" {
			current, err := nativeInstalledPackages()
			if err != nil {
				return err
			}
			verified := false
			for _, candidate := range current {
				if candidate.name == record.name && strings.EqualFold(candidate.revision, revision) {
					verified = true
				}
			}
			if !verified {
				return nativeErrorf("CAO package %s did not record requested commit %s; refusing to materialize", record.name, revision)
			}
		}
		campaign := nativePackageSlug(record.name)
		var declaration *campaignDeclaration
		if campaign == "root" || campaign == "activity" || campaign == "dashboard" {
			err = nativeMaterializeRuntime(cmd.Context(), gh, campaign)
		} else {
			declaration, err = materializeCampaign(cmd.Context(), gh, campaign, "")
		}
		if err != nil {
			return nativeErrorf("Unable to materialize CAO %s: %v", campaign, err)
		}
		if declaration != nil {
			mergeCampaignDeclaration(policy, declaration)
			declarations = append(declarations, declaration.Campaign)
		}
		updated = append(updated, record.name)
	}
	if err := pinAutoUpgradeActions(); err != nil {
		return err
	}
	if len(declarations) > 0 {
		if err := nativeValidatePolicy(policy); err != nil {
			return err
		}
		if err := writePolicy(policy, false); err != nil {
			return err
		}
	}
	result := map[string]any{"command": "update", "policy": policyPath, "gh-aw": ghAw, "campaigns": updated, "declarations": declarations}
	if revision != "" {
		result["ref"], result["resolvedCommit"] = options.ref, revision
	}
	return printJSON(cmd, result)
}

func nativePackageSlug(name string) string {
	if name == catalogRepository {
		return "root"
	}
	return name[strings.LastIndex(name, "/")+1:]
}

func nativeContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type nativePreparedFile struct {
	path              string
	original, written []byte
}

type nativePreparedPackage struct {
	files []nativePreparedFile
}

func nativePrepareRelease(ctx context.Context, gh ghExecutor, record nativePackageRecord, options nativeUpdateOptions, tags map[string]string) (*nativePreparedPackage, error) {
	ref := record.source[strings.LastIndex(record.source, "@")+1:]
	isCommit := commitPattern.MatchString(ref)
	if !isCommit && (!options.prereleases || !nativeExactVersion.MatchString(ref)) {
		return nil, nil
	}
	tag := ref
	if isCommit {
		tag = tags[record.revision]
		if tag == "" {
			revisionJSON, _ := json.Marshal(record.revision)
			stdout, stderr, err := gh(ctx, "api", "--paginate", "/repos/"+catalogRepository+"/tags", "--jq", `.[] | select(.commit.sha == `+string(revisionJSON)+`) | .name`)
			if err != nil {
				return nil, commandError("Unable to resolve CAO tag for "+record.revision, stderr, err)
			}
			for _, candidate := range strings.Fields(stdout) {
				if !nativeExactVersion.MatchString(candidate) {
					continue
				}
				filter := ".prerelease == false"
				if options.prereleases {
					filter = "true"
				}
				release, _, err := gh(ctx, "api", "/repos/"+catalogRepository+"/releases/tags/"+url.PathEscape(candidate), "--jq", "select(.draft == false and "+filter+") | .tag_name")
				if err == nil && strings.TrimSpace(release) == candidate {
					tag = candidate
					break
				}
			}
			if tag == "" {
				return nil, nativeErrorf("No CAO release tag found for %s; run \"cao update latest\" to replace installed CAO packages with the latest stable release", record.revision)
			}
			tags[record.revision] = tag
		}
	}
	if options.prereleases {
		stdout, stderr, err := gh(ctx, "api", "--paginate", "/repos/"+catalogRepository+"/releases?per_page=100", "--jq", ".[] | select(.draft == false) | .tag_name")
		if err != nil {
			return nil, commandError("Unable to list CAO releases", stderr, err)
		}
		major, _ := strconv.ParseFloat(strings.TrimPrefix(strings.SplitN(tag, ".", 2)[0], "v"), 64)
		for _, candidate := range strings.Fields(stdout) {
			candidateMajor, _ := strconv.ParseFloat(strings.TrimPrefix(strings.SplitN(candidate, ".", 2)[0], "v"), 64)
			if nativeExactVersion.MatchString(candidate) && (nativeContains(options.options, "--major") || candidateMajor == major) && nativeCompareVersions(candidate, tag) >= 0 {
				tag = candidate
			}
		}
	}
	prepared := &nativePreparedPackage{}
	write := func(filename string, content []byte) error {
		original, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filename, content, 0o644); err != nil {
			return err
		}
		prepared.files = append(prepared.files, nativePreparedFile{filename, original, content})
		return nil
	}
	record.document["source"] = record.name + "@" + tag
	files, _ := record.document["files"].([]any)
	for _, file := range files {
		declaration, ok := file.(map[string]any)
		if !ok {
			return nil, errors.Join(errors.New("installed package files must contain mappings"), nativeRestorePackage(prepared))
		}
		destination, _ := declaration["destination"].(string)
		if !strings.HasSuffix(destination, ".md") {
			continue
		}
		filename, err := repositoryPath(destination)
		if err != nil {
			return nil, errors.Join(err, nativeRestorePackage(prepared))
		}
		content, err := os.ReadFile(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Join(err, nativeRestorePackage(prepared))
		}
		prefix := "source: " + record.name + "@"
		newline := "\n"
		if strings.Contains(string(content), "\r\n") {
			newline = "\r\n"
		}
		lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
		for index, line := range lines {
			if strings.HasPrefix(line, prefix) {
				lines[index] = prefix + tag
				updated := []byte(strings.Join(lines, newline))
				if err := write(filename, updated); err != nil {
					return nil, errors.Join(err, nativeRestorePackage(prepared))
				}
				hash, err := nativeHashFile(filename)
				if err != nil {
					return nil, errors.Join(err, nativeRestorePackage(prepared))
				}
				declaration["sha256"] = hash
				break
			}
		}
	}
	content, err := json.MarshalIndent(record.document, "", "  ")
	if err == nil {
		err = write(record.filename, append(content, '\n'))
	}
	if err != nil {
		return nil, errors.Join(err, nativeRestorePackage(prepared))
	}
	return prepared, nil
}

func nativeRestorePackage(prepared *nativePreparedPackage) error {
	if prepared == nil {
		return nil
	}
	var result error
	for _, file := range prepared.files {
		content, err := os.ReadFile(file.path)
		if err == nil && string(content) == string(file.written) {
			result = errors.Join(result, os.WriteFile(file.path, file.original, 0o644))
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}
