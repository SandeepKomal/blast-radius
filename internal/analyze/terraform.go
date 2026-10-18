package analyze

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

// Terraform flags that take a value, so a following argument is not a plan file.
var tfValueFlags = set("-var", "-var-file", "-target", "-replace", "-exclude", "-state", "-state-out",
	"-backup", "-lock-timeout", "-parallelism")

// Flags of apply/destroy that also change what plan computes.
var tfPlanFlags = set("-var", "-var-file", "-target", "-replace", "-exclude", "-refresh", "-refresh-only",
	"-parallelism", "-state")

// Resource types whose deletion loses data or cascades to everything inside.
var tfStateful = set(
	"aws_db_instance", "aws_rds_cluster", "aws_rds_cluster_instance", "aws_s3_bucket",
	"aws_dynamodb_table", "aws_ebs_volume", "aws_efs_file_system", "aws_elasticache_cluster",
	"aws_elasticache_replication_group", "aws_kms_key", "aws_secretsmanager_secret",
	"aws_redshift_cluster", "aws_docdb_cluster", "aws_neptune_cluster", "aws_opensearch_domain",
	"aws_elasticsearch_domain", "aws_msk_cluster", "aws_backup_vault", "aws_ecr_repository",
	"aws_route53_zone", "aws_cognito_user_pool", "aws_kinesis_stream", "aws_sqs_queue",
	"aws_eks_cluster", "aws_fsx_lustre_file_system", "aws_timestreamwrite_database", "aws_memorydb_cluster",
	"google_sql_database_instance", "google_sql_database", "google_storage_bucket",
	"google_compute_disk", "google_bigquery_dataset", "google_bigquery_table",
	"google_spanner_instance", "google_spanner_database", "google_kms_crypto_key",
	"google_filestore_instance", "google_redis_instance", "google_dns_managed_zone",
	"google_project", "google_container_cluster", "google_alloydb_cluster", "google_firestore_database",
	"azurerm_resource_group", "azurerm_storage_account", "azurerm_mssql_database",
	"azurerm_mssql_server", "azurerm_postgresql_flexible_server", "azurerm_mysql_flexible_server",
	"azurerm_cosmosdb_account", "azurerm_key_vault", "azurerm_managed_disk", "azurerm_redis_cache",
	"azurerm_dns_zone", "azurerm_kubernetes_cluster",
	"kubernetes_namespace", "kubernetes_namespace_v1", "kubernetes_persistent_volume_claim",
	"kubernetes_persistent_volume_claim_v1", "kubernetes_persistent_volume", "kubernetes_persistent_volume_v1",
	"digitalocean_database_cluster", "digitalocean_volume", "digitalocean_spaces_bucket",
	"cloudflare_zone", "github_repository", "vault_mount", "mongodbatlas_cluster",
)

func (a *Analyzer) terraform(ctx context.Context, c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	var global []string
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		global = append(global, args[0])
		args = args[1:]
	}
	if len(args) == 0 {
		return nil
	}
	sub, rest := args[0], args[1:]

	workDir := st.dir
	for _, g := range global {
		if v, ok := strings.CutPrefix(g, "-chdir="); ok {
			workDir = resolve(st.dir, v)
		}
	}
	env := st.envFor(c)

	if sub == "workspace" && len(rest) > 0 && (rest[0] == "select" || rest[0] == "new") {
		if name := positionals(rest[1:], nil); len(name) > 0 {
			st.workspaces[workDir] = name[0]
		}
		return nil
	}

	ws := tfWorkspace(env, st, workDir)
	f := &Finding{
		Tool:    c.Program(),
		Command: c.String(),
		Target:  "workspace " + ws,
		Labels:  []string{ws, workDir},
	}

	switch sub {
	case "destroy":
		a.tfPreview(ctx, f, c, st.dir, env, global, rest, true)
	case "apply":
		a.tfPreview(ctx, f, c, st.dir, env, global, rest, hasFlag(rest, "-destroy"))
	case "state":
		if len(rest) == 0 {
			return nil
		}
		switch rest[0] {
		case "rm", "push":
			f.Level = Destroy
			f.Notes = append(f.Notes, "terraform state "+rest[0]+" rewrites state; resources can be orphaned or overwritten")
		case "mv", "replace-provider":
			f.Level = Change
		default:
			return nil
		}
	case "workspace":
		if len(rest) == 0 || rest[0] != "delete" {
			return nil
		}
		f.Level = Destroy
		f.Notes = append(f.Notes, "deletes a workspace and its state")
	case "taint", "untaint", "force-unlock", "import":
		f.Level = Change
	default:
		return nil
	}
	return f
}

// Terragrunt global flags that take a separate value.
var tgValueFlags = set("--working-dir", "--terragrunt-working-dir", "--config", "--terragrunt-config",
	"--log-level", "--terragrunt-log-level", "--tf-path", "--terragrunt-tfpath", "--download-dir",
	"--terragrunt-download-dir", "--iam-assume-role", "--terragrunt-iam-role")

// terragrunt previews single-module commands through terragrunt itself, which
// passes plan and show through to terraform. run-all commands span many
// modules and are not previewed.
func (a *Analyzer) terragrunt(ctx context.Context, c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if tgValueFlags[args[i]] {
			i++
		}
		i++
	}
	if i > len(args) {
		i = len(args)
	}
	if i == len(args) {
		return nil
	}
	global, sub, rest := args[:i], args[i], args[i+1:]
	all := sub == "run-all" || hasFlag(rest, "--all") || sub == "destroy-all" || sub == "apply-all"
	if sub == "run-all" || sub == "run" {
		if p := positionals(rest, nil); len(p) > 0 {
			sub = p[0]
		}
	}
	sub = strings.TrimSuffix(sub, "-all")

	f := &Finding{Tool: "terragrunt", Command: c.String(), Target: "directory " + st.dir, Labels: []string{st.dir}}
	switch sub {
	case "destroy", "apply":
	default:
		return nil
	}
	if all {
		f.Level = Destroy
		f.Notes = append(f.Notes, "runs across every module below this directory; blast-radius previews one module at a time, so treating this as destructive")
		return f
	}
	env := st.envFor(c)
	a.tfPreview(ctx, f, c, st.dir, env, global, rest, sub == "destroy" || hasFlag(rest, "-destroy"))
	return f
}

// tfPreview runs `terraform plan` (or `show` for a saved plan file) and fills
// f with the planned resource changes.
func (a *Analyzer) tfPreview(ctx context.Context, f *Finding, c shell.Command, dir string, env, global, args []string, destroy bool) {
	tf := c.Args[0]
	var kept, positional []string
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		if !strings.HasPrefix(args[i], "-") {
			positional = append(positional, args[i])
			continue
		}
		takesNext := tfValueFlags[name] && !hasValue && i+1 < len(args)
		if tfPlanFlags[name] {
			kept = append(kept, args[i])
			if takesNext {
				kept = append(kept, args[i+1])
			}
		}
		if takesNext {
			i++
		}
	}

	var planFile string
	if len(positional) > 0 {
		planFile = positional[0]
	} else {
		tmp, err := os.CreateTemp("", "blast-radius-*.tfplan")
		if err != nil {
			unavailable(f, "could not create a temp file", Result{}, err)
			return
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		planFile = tmp.Name()

		planArgs := append(append([]string{}, global...), "plan", "-input=false", "-lock=false", "-no-color", "-out="+planFile)
		if destroy {
			planArgs = append(planArgs, "-destroy")
		}
		planArgs = append(planArgs, kept...)
		workDir := dir
		if v, ok := flagValue(global, "-chdir"); ok {
			workDir = resolve(dir, v)
		}
		defer keepWorkspaces(workDir)()
		res, err := a.Run(ctx, dir, env, tf, planArgs...)
		if err != nil || res.ExitCode != 0 {
			unavailable(f, "terraform plan failed", res, err)
			return
		}
	}

	showArgs := append(append([]string{}, global...), "show", "-json", "-no-color", planFile)
	res, err := a.Run(ctx, dir, env, tf, showArgs...)
	if err != nil || res.ExitCode != 0 {
		unavailable(f, "terraform show failed", res, err)
		return
	}
	if err := a.applyPlan(f, res.Stdout); err != nil {
		unavailable(f, "could not read the plan", Result{}, err)
	}
}

type tfPlan struct {
	ResourceChanges []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Mode    string `json:"mode"`
		Change  struct {
			Actions []string `json:"actions"`
		} `json:"change"`
	} `json:"resource_changes"`
}

func (a *Analyzer) applyPlan(f *Finding, data []byte) error {
	// terragrunt can print log lines before the JSON document.
	if i := strings.IndexByte(string(data), '{'); i > 0 {
		data = data[i:]
	}
	var plan tfPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	f.Level = None
	for _, rc := range plan.ResourceChanges {
		if rc.Mode == "data" {
			continue
		}
		var act string
		switch strings.Join(rc.Change.Actions, ",") {
		case "delete":
			act = ActDestroy
		case "delete,create", "create,delete":
			act = ActReplace
		case "update":
			act = ActUpdate
		case "create":
			act = ActCreate
		case "forget":
			act = ActForget
		default:
			continue // no-op, read
		}
		r := Resource{Action: act, Name: rc.Address}
		r.Stateful = r.Lost() && a.stateful(rc.Type)
		f.Resources = append(f.Resources, r)
		if r.Lost() {
			f.Level = Destroy
		} else if f.Level < Change {
			f.Level = Change
		}
	}
	return nil
}

// tfWorkspace finds the selected workspace the same way terraform does,
// including workspaces selected earlier in the same script.
func tfWorkspace(env []string, st *state, workDir string) string {
	if ws := lookup(env, "TF_WORKSPACE"); ws != "" {
		return ws
	}
	if ws, ok := st.workspaces[workDir]; ok {
		return ws
	}
	dataDir := lookup(env, "TF_DATA_DIR")
	if dataDir == "" {
		dataDir = ".terraform"
	}
	if b, err := os.ReadFile(filepath.Join(resolve(workDir, dataDir), "environment")); err == nil {
		if ws := strings.TrimSpace(string(b)); ws != "" {
			return ws
		}
	}
	return "default"
}

// keepWorkspaces undoes a side effect of planning in a workspace that does
// not exist yet: with the local backend, terraform creates an empty
// terraform.tfstate.d/<name> directory, after which the workspace appears to
// exist. The returned function removes such directories if they are still
// empty. Directories that existed before, or that now hold state, are kept.
func keepWorkspaces(workDir string) func() {
	root := filepath.Join(workDir, "terraform.tfstate.d")
	before := map[string]bool{}
	entries, err := os.ReadDir(root)
	rootExisted := err == nil
	for _, e := range entries {
		before[e.Name()] = true
	}
	return func() {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() && !before[e.Name()] {
				os.Remove(filepath.Join(root, e.Name())) // fails, harmlessly, unless empty
			}
		}
		if !rootExisted {
			os.Remove(root)
		}
	}
}
