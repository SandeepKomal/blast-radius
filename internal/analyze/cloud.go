package analyze

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

// verbIn reports whether any argument is one of verbs.
func verbIn(pos []string, verbs map[string]bool) (string, bool) {
	for _, p := range pos {
		if verbs[p] {
			return p, true
		}
	}
	return "", false
}

func cloudFinding(tool string, c shell.Command, target string, labels []string) *Finding {
	return &Finding{Tool: tool, Command: c.String(), Target: target, Labels: labels}
}

func noUndo(f *Finding, tool string) {
	f.Notes = append(f.Notes, tool+" has no preview; this takes effect immediately and can't be undone")
}

var pulumiValueFlags = set("-s", "--stack", "-C", "--cwd", "--config-file", "-m", "--message", "--parallel", "-p")

func (a *Analyzer) pulumi(ctx context.Context, c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	pos := positionals(args, pulumiValueFlags)
	if len(pos) == 0 || hasFlag(args, "--preview-only") {
		return nil
	}
	dir := st.dir
	if v, ok := flagValue(args, "-C", "--cwd"); ok {
		dir = resolve(st.dir, v)
	}
	// `stack select` and `stack init` change the stack later commands use.
	if len(pos) == 3 && pos[0] == "stack" && (pos[1] == "select" || pos[1] == "init") {
		st.pulumiStacks[dir] = pos[2]
		return nil
	}
	if len(pos) == 2 && pos[0] == "stack" && (pos[1] == "select" || pos[1] == "init") {
		if v, ok := flagValue(args, "-s", "--stack"); ok {
			st.pulumiStacks[dir] = v
		}
		return nil
	}
	stack := a.pulumiStack(ctx, c, st, dir)
	if len(pos) == 3 && pos[0] == "stack" && pos[1] == "rm" {
		stack = pos[2] // the stack being removed, not the selected one
	}
	f := cloudFinding("pulumi", c, "stack "+stack, []string{stack, dir})
	switch {
	case pos[0] == "destroy":
		f.Level = Destroy
		f.Notes = append(f.Notes, "deletes every resource in the stack")
	case pos[0] == "up" || pos[0] == "update":
		f.Level = Change
		f.Notes = append(f.Notes, "no preview yet for pulumi up; run `pulumi preview` to see what it deletes")
	case pos[0] == "stack" && len(pos) > 1 && pos[1] == "rm":
		f.Level = Destroy
		f.Notes = append(f.Notes, "removes the stack and its state")
	case pos[0] == "state" && len(pos) > 1 && pos[1] == "delete":
		f.Level = Destroy
		f.Notes = append(f.Notes, "removes resources from state; they are no longer managed")
	case pos[0] == "refresh" || pos[0] == "import":
		f.Level = Change
	default:
		return nil
	}
	return f
}

// pulumiStack works out which stack a command runs against: --stack,
// PULUMI_STACK, a stack selected earlier in the script, or the stack Pulumi
// has selected for the project.
func (a *Analyzer) pulumiStack(ctx context.Context, c shell.Command, st *state, dir string) string {
	if v, ok := flagValue(c.Args[1:], "-s", "--stack"); ok {
		return v
	}
	env := st.envFor(c)
	if v := lookup(env, "PULUMI_STACK"); v != "" {
		return v
	}
	if v, ok := st.pulumiStacks[dir]; ok {
		return v
	}
	res, err := a.Run(ctx, dir, env, c.Args[0], "stack", "--show-name", "--non-interactive")
	if err == nil && res.ExitCode == 0 {
		if v := strings.TrimSpace(string(res.Stdout)); v != "" {
			return v
		}
	}
	return "unknown"
}

var awsValueFlags = set("--profile", "--region", "--output", "--endpoint-url", "--query", "--color",
	"--ca-bundle", "--cli-read-timeout", "--cli-connect-timeout", "--cli-binary-format")

var awsDestroyPrefixes = []string{"delete-", "terminate-", "remove-", "deregister-", "purge-", "schedule-key-deletion", "disable-key"}

var awsChangePrefixes = []string{"create-", "put-", "update-", "modify-", "run-", "start-", "stop-", "reboot-",
	"attach-", "detach-", "associate-", "disassociate-", "register-", "tag-", "untag-", "set-", "enable-",
	"import-", "restore-", "replace-", "reset-", "revoke-", "authorize-", "invoke", "send-", "publish"}

// Services whose deletions lose data, and S3 commands that delete objects.
var awsStatefulServices = set("rds", "dynamodb", "s3", "s3api", "efs", "kms", "secretsmanager", "ecr",
	"elasticache", "redshift", "opensearch", "es", "backup", "docdb", "neptune", "fsx", "memorydb", "eks",
	"glacier", "timestream-write", "keyspaces")

func (a *Analyzer) aws(c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	pos := positionals(args, awsValueFlags)
	if len(pos) < 2 || hasFlag(args, "--dry-run") {
		return nil
	}
	svc, op := pos[0], pos[1]
	env := st.envFor(c)
	profile, ok := flagValue(args, "--profile")
	if !ok {
		profile = lookup(env, "AWS_PROFILE")
	}
	if profile == "" {
		profile = lookup(env, "AWS_DEFAULT_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	region, ok := flagValue(args, "--region")
	if !ok {
		region = lookup(env, "AWS_REGION")
	}
	if region == "" {
		region = lookup(env, "AWS_DEFAULT_REGION")
	}
	target := "profile " + profile
	if region != "" {
		target += ", region " + region
	}
	f := cloudFinding("aws", c, target, []string{profile, region, st.dir})

	switch {
	case svc == "s3" && (op == "rm" || op == "rb"):
		f.Level = Destroy
	case svc == "s3" && op == "sync" && hasFlag(args, "--delete"):
		f.Level = Destroy
	case svc == "s3" && (op == "cp" || op == "mv" || op == "sync"):
		if dest := pos[len(pos)-1]; strings.HasPrefix(dest, "s3://") || op == "mv" {
			f.Level = Change
		}
	case hasPrefix(op, awsDestroyPrefixes):
		f.Level = Destroy
	case hasPrefix(op, awsChangePrefixes):
		f.Level = Change
	}
	if f.Level == None {
		return nil
	}
	name := "aws " + svc + " " + op
	if len(pos) > 2 {
		name += " " + pos[2]
	}
	if f.Level == Destroy {
		f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: name, Stateful: awsStatefulServices[svc]})
		noUndo(f, "the aws CLI")
	}
	return f
}

var gcloudValueFlags = set("--project", "--region", "--zone", "--format", "--account", "--configuration",
	"--impersonate-service-account", "--billing-project", "--verbosity", "--filter", "--location")

var (
	gcloudDestroy = set("delete", "remove", "rm", "purge")
	gcloudChange  = set("create", "update", "deploy", "patch", "set", "resize", "stop", "reset", "restart",
		"import", "scale", "add-iam-policy-binding", "remove-iam-policy-binding", "set-iam-policy", "cp", "mv")
	gcloudStateful = set("sql", "storage", "disks", "snapshots", "clusters", "projects", "spanner",
		"filestore", "redis", "bigtable", "firestore", "alloydb", "kms", "secrets", "buckets")
)

func (a *Analyzer) gcloud(c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	pos := positionals(args, gcloudValueFlags)
	if len(pos) == 4 && pos[0] == "config" && pos[1] == "set" && (pos[2] == "project" || pos[2] == "core/project") {
		st.gcloudProject = pos[3]
		return nil
	}
	project := gcloudProject(args, st, st.envFor(c))
	f := cloudFinding("gcloud", c, "project "+project, []string{project, st.dir})
	if verb, ok := verbIn(pos, gcloudDestroy); ok {
		f.Level = Destroy
		group := strings.Join(pos[:indexOf(pos, verb)], " ")
		f.Resources = append(f.Resources, Resource{
			Action:   ActDestroy,
			Name:     "gcloud " + strings.Join(pos, " "),
			Stateful: anyIn(strings.Fields(group), gcloudStateful),
		})
		noUndo(f, "gcloud")
		return f
	}
	if _, ok := verbIn(pos, gcloudChange); ok {
		f.Level = Change
		return f
	}
	return nil
}

// gcloudProject works out the project a gcloud command runs against:
// --project, CLOUDSDK_CORE_PROJECT, `gcloud config set project` earlier in
// the script, or the active gcloud configuration on disk.
func gcloudProject(args []string, st *state, env []string) string {
	if v, ok := flagValue(args, "--project"); ok {
		return v
	}
	if v := lookup(env, "CLOUDSDK_CORE_PROJECT"); v != "" {
		return v
	}
	if st.gcloudProject != "" {
		return st.gcloudProject
	}
	dir := lookup(env, "CLOUDSDK_CONFIG")
	if dir == "" {
		dir = gcloudConfigDir()
	}
	if dir == "" {
		return "default"
	}
	name, ok := flagValue(args, "--configuration")
	if !ok {
		name = lookup(env, "CLOUDSDK_ACTIVE_CONFIG_NAME")
	}
	if name == "" {
		b, _ := os.ReadFile(filepath.Join(dir, "active_config"))
		name = strings.TrimSpace(string(b))
	}
	if name == "" {
		name = "default"
	}
	b, err := os.ReadFile(filepath.Join(dir, "configurations", "config_"+name))
	if err != nil {
		return "default"
	}
	if v := iniValue(string(b), "core", "project"); v != "" {
		return v
	}
	return "default"
}

func gcloudConfigDir() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("APPDATA"); v != "" {
			return filepath.Join(v, "gcloud")
		}
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gcloud")
}

// iniValue returns key from [section] of an INI file.
func iniValue(text, section, key string) string {
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.TrimSpace(line[1:len(line)-1]) == section
			continue
		}
		if k, v, ok := strings.Cut(line, "="); in && ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (a *Analyzer) gsutil(c shell.Command, st *state) *Finding {
	pos := positionals(c.Args[1:], set("-o", "-h", "-p", "-u"))
	if len(pos) == 0 {
		return nil
	}
	f := cloudFinding("gsutil", c, "Cloud Storage", []string{st.dir})
	switch pos[0] {
	case "rm", "rb":
		f.Level = Destroy
	case "rsync":
		if hasFlag(c.Args[1:], "-d") {
			f.Level = Destroy
		} else {
			f.Level = Change
		}
	case "cp", "mv":
		f.Level = Change
	default:
		return nil
	}
	if f.Level == Destroy {
		f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: "gsutil " + strings.Join(pos, " "), Stateful: true})
		noUndo(f, "gsutil")
	}
	return f
}

var azValueFlags = set("--subscription", "-s", "-g", "--resource-group", "-n", "--name", "-o", "--output", "--query", "-l", "--location")

var (
	azDestroy  = set("delete", "purge", "remove")
	azChange   = set("create", "update", "set", "start", "stop", "restart", "deploy", "upgrade", "scale", "import", "add", "assign")
	azStateful = set("group", "sql", "storage", "cosmosdb", "keyvault", "disk", "snapshot", "aks", "postgres",
		"mysql", "redis", "backup", "acr")
)

func (a *Analyzer) az(c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	pos := positionals(args, azValueFlags)
	if len(pos) == 2 && pos[0] == "account" && pos[1] == "set" {
		if v, ok := flagValue(args, "--subscription", "-s"); ok {
			st.azSubscription = v
		}
		return nil
	}
	sub := azSubscription(args, st, st.envFor(c))
	target := "subscription " + sub
	labels := []string{sub, st.dir}
	rg, ok := flagValue(args, "-g", "--resource-group")
	if !ok && len(pos) > 0 && pos[0] == "group" {
		// `az group delete -n prod-rg` names the resource group with -n.
		rg, ok = flagValue(args, "-n", "--name")
	}
	if ok {
		target += ", resource group " + rg
		labels = append(labels, rg)
	}
	f := cloudFinding("az", c, target, labels)
	if verb, ok := verbIn(pos, azDestroy); ok {
		f.Level = Destroy
		group := pos[:indexOf(pos, verb)]
		name, _ := flagValue(args, "-n", "--name")
		r := "az " + strings.Join(pos, " ")
		if name != "" {
			r += " " + name
		}
		f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: r, Stateful: anyIn(group, azStateful)})
		if len(group) > 0 && group[0] == "group" {
			f.Notes = append(f.Notes, "deleting a resource group deletes everything in it")
		}
		noUndo(f, "the az CLI")
		return f
	}
	if _, ok := verbIn(pos, azChange); ok {
		f.Level = Change
		return f
	}
	return nil
}

// azSubscription works out the subscription an az command runs against:
// --subscription, `az account set` earlier in the script, or the default
// subscription in the Azure CLI profile on disk.
func azSubscription(args []string, st *state, env []string) string {
	if v, ok := flagValue(args, "--subscription"); ok {
		return v
	}
	if st.azSubscription != "" {
		return st.azSubscription
	}
	dir := lookup(env, "AZURE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "default"
		}
		dir = filepath.Join(home, ".azure")
	}
	b, err := os.ReadFile(filepath.Join(dir, "azureProfile.json"))
	if err != nil {
		return "default"
	}
	var profile struct {
		Subscriptions []struct {
			Name      string `json:"name"`
			IsDefault bool   `json:"isDefault"`
		} `json:"subscriptions"`
	}
	// The Azure CLI writes this file with a byte order mark.
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &profile) != nil {
		return "default"
	}
	for _, s := range profile.Subscriptions {
		if s.IsDefault && s.Name != "" {
			return s.Name
		}
	}
	return "default"
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return len(list)
}

func anyIn(list []string, m map[string]bool) bool {
	for _, v := range list {
		if m[v] {
			return true
		}
	}
	return false
}
