package topology

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

func jsonBytes(value any) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	return append(content, '\n'), err
}

func filesFor(ctx context.Context, content []byte) (map[string][]byte, Topology, error) {
	input, err := Parse(content)
	if err != nil {
		return nil, Topology{}, err
	}
	solution, err := Solve(ctx, input)
	if err != nil {
		return nil, Topology{}, err
	}
	files, err := deploymentFiles(input.Features)
	if err != nil {
		return nil, Topology{}, err
	}
	report := ReviewRules(solution)
	if err := report.Err(); err != nil {
		return nil, Topology{}, err
	}
	for name, value := range map[string]any{"topology.json": solution.Topology, "parameters.json": solution.Parameters} {
		files[name], err = jsonBytes(value)
		if err != nil {
			return nil, Topology{}, err
		}
	}
	files["topology.yaml"] = solution.TOSCA
	files["AGENT-MCP.md"], err = assets.ReadFile("assets/AGENT-MCP.md")
	if err != nil {
		return nil, Topology{}, err
	}
	files["requirements.yaml"], err = yaml.Marshal(input)
	if err != nil {
		return nil, Topology{}, err
	}
	files["rules.json"], err = jsonBytes(report)
	if err != nil {
		return nil, Topology{}, err
	}
	files["RULES.md"] = []byte(report.Markdown())
	files["MANIFEST.md"] = []byte(renderManifest(solution.Topology) + fmt.Sprintf("\n## Architecture rules\n\n%d strict errors; %d advisory warnings. See [RULES.md](RULES.md).\nStatic declared topology only; not a security certification or live assessment.\n", report.Errors, report.Warnings))
	documentDirectory := "assets/docs"
	if input.Features.Deployment != "azure-functions" {
		documentDirectory = "assets/compact-docs"
	}
	if input.Features.Deployment != "azure-functions" || len(additionalRuntimeSecrets(input.Features)) > 0 {
		references := map[string]*SecretReference{}
		for name, secret := range input.Azure.Secrets {
			ref := &SecretReference{SecretName: secret}
			ref.KeyVault.ID = input.Azure.SecretVaultID
			references[name] = ref
		}
		files["runtime-secret-references.json"], err = jsonBytes(references)
		if err != nil {
			return nil, Topology{}, err
		}
	}
	documents, err := fs.ReadDir(assets, documentDirectory)
	if err != nil {
		return nil, Topology{}, err
	}
	for _, entry := range documents {
		name := strings.TrimSuffix(entry.Name(), ".tmpl")
		if name == "OPTIONAL-FEATURES.md" && solution.Optional == nil {
			continue
		}
		source, err := assets.ReadFile(documentDirectory + "/" + entry.Name())
		if err != nil {
			return nil, Topology{}, err
		}
		document, err := template.New(name).Funcs(template.FuncMap{"join": strings.Join}).Option("missingkey=error").Parse(string(source))
		if err != nil {
			return nil, Topology{}, err
		}
		var rendered bytes.Buffer
		if err := document.Execute(&rendered, solution); err != nil {
			return nil, Topology{}, err
		}
		files[name] = rendered.Bytes()
	}
	files["scripts/what-if.sh"] = []byte(whatIfScript("main.bicep", "parameters.json"))
	files[".gitignore"] = []byte("main.json\n")
	if solution.Optional != nil {
		files["optional/parameters.json"], err = jsonBytes(solution.Optional)
		if err != nil {
			return nil, Topology{}, err
		}
		entries, err := assets.ReadDir("assets/azure")
		if err != nil {
			return nil, Topology{}, err
		}
		for _, entry := range entries {
			files["optional/"+entry.Name()], err = assets.ReadFile("assets/azure/" + entry.Name())
			if err != nil {
				return nil, Topology{}, err
			}
		}
		files["scripts/what-if-optional.sh"] = []byte(whatIfScript("optional/hosts.bicep", "optional/parameters.json"))
		files[".gitignore"] = append(files[".gitignore"], []byte("optional/hosts.json\n")...)
		if input.Features.WebhookGateway {
			files["scripts/run-gateway.sh"], err = assets.ReadFile("assets/run-gateway.sh")
			if err != nil {
				return nil, Topology{}, err
			}
		}
	}
	var validationScript strings.Builder
	validationScript.WriteString("#!/bin/sh\nset -eu\ncd \"$(CDPATH='' cd -- \"$(dirname -- \"$0\")/..\" && pwd)\"\n")
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if strings.HasSuffix(name, ".bicep") {
			fmt.Fprintf(&validationScript, "bicep build '%s' --no-restore --stdout > /dev/null\n", strings.ReplaceAll(name, "'", "'\"'\"'"))
		}
	}
	files["scripts/validate.sh"] = []byte(validationScript.String())
	hashes := map[string]string{}
	for name, content := range files {
		sum := sha256.Sum256(content)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	files["manifest.json"], err = jsonBytes(map[string]any{
		"version": 1, "backend": "azure", "engine": "puccini v0.22.7", "profile": "tosca_simple_yaml_1_3", "files": hashes,
	})
	return files, solution.Topology, err
}

func whatIfScript(bicep, parameters string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$#" -ne 2 ]; then
  echo "Usage: sh $0 SUBSCRIPTION_ID RESOURCE_GROUP" >&2
  exit 2
fi
cd "$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
az deployment group what-if --subscription "$1" --resource-group "$2" \
  --template-file %s --parameters @%s
`, bicep, parameters)
}

func writeExclusive(name string, content []byte) error {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func Init(destination string) error {
	return InitExample(destination, "azure-functions")
}

func InitExample(destination, deployment string) error {
	content, err := ExampleFor(deployment)
	if err != nil {
		return err
	}
	return writeExclusive(destination, content)
}

func Generate(ctx context.Context, content []byte, directory string, validator BicepValidator) (result Topology, err error) {
	if validator == nil {
		return result, fmt.Errorf("Bicep validation is required for generation")
	}
	files, result, err := filesFor(ctx, content)
	if err != nil {
		return result, err
	}
	if _, err := os.Lstat(directory); err == nil {
		return result, fmt.Errorf("output directory already exists: %s", directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(directory), ".cao-topology-")
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	if err := writeBundle(ctx, files, stage); err != nil {
		return result, err
	}
	templates := []string{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if strings.HasSuffix(name, ".bicep") {
			templates = append(templates, name)
		}
	}
	if err := validator.Validate(ctx, stage, templates); err != nil {
		return result, err
	}
	if _, err := Check(ctx, stage); err != nil {
		return result, err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return result, err
	}
	if err := writeBundle(ctx, files, directory); err != nil {
		return result, err
	}
	return Check(ctx, directory)
}

func writeBundle(ctx context.Context, files map[string][]byte, directory string) error {
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := writeExclusive(target, files[name]); err != nil {
			return err
		}
	}
	return nil
}

func ReadRequirements(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("requirements must be a regular file at most 1 MiB")
	}
	return os.ReadFile(filename)
}

func Check(ctx context.Context, directory string) (Topology, error) {
	var result Topology
	content, err := ReadRequirements(filepath.Join(directory, "requirements.yaml"))
	if err != nil {
		return result, err
	}
	files, expected, err := filesFor(ctx, content)
	if err != nil {
		return result, err
	}
	allowed := map[string]bool{"main.json": true}
	for name := range files {
		allowed[name] = true
	}
	if _, exists := files["optional/hosts.bicep"]; exists {
		allowed["optional/hosts.json"] = true
	}
	if err := filepath.WalkDir(directory, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if entry.IsDir() {
			if name == "." {
				return nil
			}
			for candidate := range allowed {
				if strings.HasPrefix(candidate, name+"/") {
					return nil
				}
			}
		} else if entry.Type().IsRegular() && allowed[name] {
			return nil
		}
		return fmt.Errorf("unexpected or non-regular generated entry: %s", name)
	}); err != nil {
		return result, err
	}
	content, err = os.ReadFile(filepath.Join(directory, "topology.json"))
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := CheckTopology(result); err != nil {
		return result, err
	}
	if !reflect.DeepEqual(result, expected) {
		return result, fmt.Errorf("topology does not match requirements")
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil {
			return result, err
		}
		if !bytes.Equal(content, files[name]) {
			return result, fmt.Errorf("generated bundle drift or misconfiguration: %s", path.Clean(name))
		}
	}
	return result, nil
}
