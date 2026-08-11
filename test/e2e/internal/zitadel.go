package e2eutil

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	// ZitadelNamespace is Zitadel's own dedicated namespace, matching the existing
	// cert-manager/Prometheus-operator/NATS/Traefik convention.
	ZitadelNamespace       = "zitadel"
	zitadelReleaseName     = "zitadel"
	zitadelChartRepoURL    = "https://charts.zitadel.com"
	zitadelPostgresCluster = "zitadel-pg"

	// zitadelMachineUsername is the FirstInstance-bootstrapped machine (service) user's
	// username — it doubles as the name of the Kubernetes Secret the chart's setup Job
	// writes the user's Personal Access Token into (see zitadelMachinePatSecretName below).
	zitadelMachineUsername = "devcluster-bootstrap"

	// zitadelMachinePatSecretName is where the zitadel/zitadel chart's setup Job stores the
	// FirstInstance-bootstrapped machine user's Personal Access Token: a Kubernetes Secret
	// named "<Machine.Username>-pat" with the token under key "pat". This is the chart's own
	// convention (templates/job_setup.yaml) — the chart explicitly refuses
	// (`helm template`/`install` fails with an explicit error) to let a caller set
	// configmapConfig.FirstInstance.PatPath itself; the path is chart-managed internally and
	// only the resulting Secret is meant to be read externally.
	zitadelMachinePatSecretName = zitadelMachineUsername + "-pat"
)

// IsZitadelInstalled reports whether the standalone "zitadel" Helm release already exists in
// its namespace.
func IsZitadelInstalled() bool {
	_, err := Run(exec.Command("helm", "status", zitadelReleaseName, "--namespace", ZitadelNamespace))
	return err == nil
}

// randomSecret returns a URL-safe random string of at least n bytes of entropy, suitable for
// Zitadel's masterkey (must be exactly 32 bytes) or a generated password.
func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("e2eutil: generate random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ensureZitadelPostgresCluster creates (if absent) a dedicated single-instance CloudNativePG
// Cluster for Zitadel in ZitadelNamespace — independent of the platform's own Postgres
// Cluster (postgres.go), same pattern, own lifecycle. Returns the generated app Secret's
// name, whose "uri" key is a ready-to-use Postgres DSN (same convention already proven by
// EnsurePostgresCluster/postgres.existingSecret).
func ensureZitadelPostgresCluster() (secretName string, err error) {
	if err := EnsureNamespace(ZitadelNamespace); err != nil {
		return "", err
	}

	secretName = zitadelPostgresCluster + "-app"

	manifest := fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  instances: 1
  storage:
    size: 1Gi
`, zitadelPostgresCluster, ZitadelNamespace)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return "", fmt.Errorf("e2eutil: apply Zitadel CloudNativePG Cluster: %w", err)
	}

	_, err = Run(exec.Command("kubectl", "wait", fmt.Sprintf("cluster.postgresql.cnpg.io/%s", zitadelPostgresCluster),
		"--namespace", ZitadelNamespace,
		"--for", "condition=Ready",
		"--timeout", "5m",
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: wait for Zitadel CloudNativePG Cluster ready: %w", err)
	}
	return secretName, nil
}

// installZitadelHelmRelease installs the zitadel/zitadel Helm chart against its own dedicated
// Postgres Cluster, bootstrapping (via the chart's declarative FirstInstance config) an Org, a
// human admin user (the printed browser-login test user), and a machine user with a
// long-lived Personal Access Token used only to authenticate Task 4's post-install
// Management-API bootstrap calls. externalPort must match whatever local port devcluster
// port-forwards Zitadel's Service to (Task 5) — Zitadel embeds it in every absolute URL it
// generates (its OIDC discovery document, redirect targets, etc.), so a mismatch breaks the
// login flow, not just cosmetics.
func installZitadelHelmRelease(externalPort int, humanUsername, humanPassword string) error {
	pgSecret, err := ensureZitadelPostgresCluster()
	if err != nil {
		return err
	}

	masterkey, err := randomSecret(32)
	if err != nil {
		return err
	}
	// Zitadel's masterkey must be exactly 32 bytes — base64 encoding a 32-byte random buffer
	// produces a longer string, so truncate to exactly 32 characters instead of using
	// randomSecret's own default entropy-sized output.
	masterkey = masterkey[:32]

	if _, err := Run(exec.Command("helm", "repo", "add", "zitadel", zitadelChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add zitadel helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "zitadel")); err != nil {
		return fmt.Errorf("e2eutil: update zitadel helm repo: %w", err)
	}

	args := []string{
		"upgrade", "--install", zitadelReleaseName, "zitadel/zitadel",
		"--namespace", ZitadelNamespace, "--create-namespace",
		"--set", "zitadel.masterkey=" + masterkey,
		"--set", "replicaCount=1",
		"--set", fmt.Sprintf("env[0].name=ZITADEL_DATABASE_POSTGRES_DSN"),
		"--set", fmt.Sprintf("env[0].valueFrom.secretKeyRef.name=%s", pgSecret),
		"--set", "env[0].valueFrom.secretKeyRef.key=uri",
		"--set", "zitadel.configmapConfig.ExternalDomain=localhost",
		"--set", fmt.Sprintf("zitadel.configmapConfig.ExternalPort=%d", externalPort),
		"--set", "zitadel.configmapConfig.ExternalSecure=false",
		"--set", "zitadel.configmapConfig.TLS.Enabled=false",
		// WARNING: Org.Human is not in the zitadel/zitadel chart's own values.yaml/schema —
		// `helm show values` only documents Org.Machine. configmapConfig is a freeform
		// passthrough straight into ZITADEL's own setup-step config (see ZITADEL's
		// cmd/defaults.yaml upstream), so Org.Human works today (confirmed against a live
		// install with chart 10.0.4/appVersion v4.15.3 — see task-3-report.md) purely because
		// ZITADEL's binary still accepts it, not because this chart promises to. No version is
		// pinned here, so a future `helm repo update` could silently stop creating the human
		// user if this field moves or is removed upstream. If FirstInstance human bootstrap
		// stops working, check ZITADEL's own defaults.yaml for a renamed/relocated field before
		// assuming it's a local bug.
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.UserName=" + humanUsername,
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.Email.Address=" + humanUsername + "@timadorus.local",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.Email.Verified=true",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.Password=" + humanPassword,
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.PasswordChangeRequired=false",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.Machine.Username=" + zitadelMachineUsername,
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.Machine.Name=devcluster bootstrap",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.MachineKey.ExpirationDate=2099-01-01T00:00:00Z",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.MachineKey.Type=1",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.Pat.ExpirationDate=2099-01-01T00:00:00Z",
		"--wait", "--timeout", "10m",
	}

	if _, err := Run(exec.Command("helm", args...)); err != nil {
		return fmt.Errorf("e2eutil: install zitadel: %w", err)
	}
	return nil
}

// readZitadelPAT retrieves the FirstInstance-bootstrapped machine user's Personal Access
// Token from the Kubernetes Secret the zitadel/zitadel chart's own setup Job writes it into
// (zitadelMachinePatSecretName, key "pat"). The chart manages this path internally — its
// templates/job_setup.yaml explicitly fails the release if configmapConfig.FirstInstance.PatPath
// is set by the caller ("Specifying ... is not supported") — so a Secret read, not a
// kubectl-exec-and-cat against a running pod's filesystem, is the chart's actual, only
// supported way to retrieve this value. The setup Job (not the long-running zitadel
// Deployment pod) is what generates it, and the Job's own pod is not exec-able once
// complete, which is the other reason a Secret read is required here.
func readZitadelPAT() (string, error) {
	pat, err := Run(exec.Command("kubectl", "get", "secret", zitadelMachinePatSecretName,
		"-n", ZitadelNamespace,
		"-o", "jsonpath={.data.pat}"))
	if err != nil {
		return "", fmt.Errorf("e2eutil: read zitadel PAT secret: %w", err)
	}
	if strings.TrimSpace(pat) == "" {
		return "", fmt.Errorf("e2eutil: read zitadel PAT secret: secret %q has no \"pat\" data", zitadelMachinePatSecretName)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pat))
	if err != nil {
		return "", fmt.Errorf("e2eutil: decode zitadel PAT secret: %w", err)
	}
	return strings.TrimSpace(string(decoded)), nil
}

// UninstallZitadel removes the Zitadel release, its dedicated CloudNativePG Cluster, and its
// namespace.
func UninstallZitadel() {
	_, _ = Run(exec.Command("kubectl", "delete", "cluster.postgresql.cnpg.io", zitadelPostgresCluster,
		"--namespace", ZitadelNamespace, "--ignore-not-found"))
	_, _ = Run(exec.Command("helm", "uninstall", zitadelReleaseName, "--namespace", ZitadelNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", ZitadelNamespace, "--ignore-not-found"))
}

// ZitadelBootstrap is everything the rest of devcluster needs after Zitadel is fully
// provisioned: a real Org, a human test user, a public/PKCE OIDC Application for the browser
// SPA, and a Machine User (see bootstrapZitadelProject's doc comment for why this is a Machine
// User and not a second Application) for machine-to-machine client_credentials access.
type ZitadelBootstrap struct {
	Authority       string // OIDC issuer / authority URL, e.g. http://localhost:8084
	JWKSURL         string
	SPAClientID     string // public/PKCE application, for web.config.oidc.clientId
	APIClientID     string // Machine User's username, for client_credentials
	APIClientSecret string
	TestUsername    string
	TestPassword    string
}

// zitadelAPICall POSTs a Connect-RPC-over-HTTP request to Zitadel's v2 Management API,
// authenticated with the FirstInstance machine user's PAT, and decodes the JSON response into
// out. authority is Zitadel's own externally-reachable base URL (matches
// installZitadelHelmRelease's externalPort).
func zitadelAPICall(authority, pat, method string, reqBody, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("e2eutil: marshal zitadel request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, authority+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("e2eutil: build zitadel request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+pat)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("e2eutil: call zitadel %s: %w", method, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("e2eutil: read zitadel %s response: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("e2eutil: zitadel %s: HTTP %d: %s", method, resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("e2eutil: decode zitadel %s response: %w", method, err)
		}
	}
	return nil
}

// bootstrapZitadelProject creates a Project with a PKCE public Application for the browser SPA,
// plus a separate Machine User with a generated client secret for machine-to-machine
// client_credentials scripted/curl access, via Zitadel's Management API v2, authenticated with
// the FirstInstance machine user's PAT. organizationId is required by CreateProject — fetched
// by listing the orgs visible to the same PAT's own identity (the machine user's default org,
// i.e. the one FirstInstance created).
//
// The API client is a Machine User, not a second Project Application, even though the brief
// originally called for "a confidential OIDC Application (client_credentials, ...)" — see the
// CORRECTION comment inline below (and task-4-report.md) for the live evidence that ZITADEL's
// client_credentials grant only ever recognizes Machine User credentials, never an
// Application's clientId, regardless of the Application's type or grant-type config.
func bootstrapZitadelProject(authority, pat, spaRedirectURI, spaPostLogoutURI string) (spaClientID, apiClientID, apiClientSecret string, err error) {
	// The machine user's own org — FirstInstance creates exactly one org, so listing the orgs
	// visible to the PAT's own identity is more robust than assuming a fixed org name/ID.
	//
	// CORRECTION (evidence-based, see task-4-report.md): the brief called
	// "/zitadel.auth.v1.AuthService/GetMyOrg" here. That is a v1 Auth API method, and v1
	// services (auth/v1, management/v1, admin/v1) are exposed ONLY via grpc-gateway REST
	// endpoints (confirmed working: "GET /auth/v1/users/me", "GET /management/v1/orgs/me"),
	// not via Connect-RPC-over-HTTP-JSON — "POST /zitadel.auth.v1.AuthService/GetMyOrg"
	// returns a real "HTTP 404: {\"code\":5, \"message\":\"Not Found\"}" body against a live
	// instance. Connect-RPC (what zitadelAPICall speaks) is only wired up for v2+ services.
	// The v2 equivalent, ListOrganizations, IS Connect-RPC and returns the identical org ID
	// (cross-checked live against both "GET /auth/v1/users/me"'s "resourceOwner" and
	// "GET /management/v1/orgs/me"'s "id" — all three agree), so it's used here instead.
	var orgs struct {
		Result []struct {
			Id string `json:"id"`
		} `json:"result"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.org.v2.OrganizationService/ListOrganizations", map[string]any{}, &orgs); err != nil {
		return "", "", "", fmt.Errorf("look up bootstrap org: %w", err)
	}
	if len(orgs.Result) == 0 {
		return "", "", "", fmt.Errorf("look up bootstrap org: ListOrganizations returned no organizations")
	}
	organizationId := orgs.Result[0].Id

	// CORRECTION (evidence-based, see task-4-report.md): the brief expected the created
	// project's ID under response field "id". Live: "POST .../CreateProject" actually returns
	// {"projectId":"...", "creationDate":"..."} — the field is "projectId", not "id".
	var project struct {
		ProjectId string `json:"projectId"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.project.v2.ProjectService/CreateProject",
		map[string]string{"organizationId": organizationId, "name": "timadorus-dev"}, &project); err != nil {
		return "", "", "", fmt.Errorf("create project: %w", err)
	}

	// CORRECTION (evidence-based, see task-4-report.md): the brief expected a top-level
	// "clientId" field on the CreateApplication response. Live: for an OIDC application the
	// response is {"applicationId":"...", "creationDate":"...", "oidcConfiguration":
	// {"clientId":"..."}} — clientId is nested under "oidcConfiguration", not top-level.
	var spaApp struct {
		ApplicationId     string `json:"applicationId"`
		OidcConfiguration struct {
			ClientId string `json:"clientId"`
		} `json:"oidcConfiguration"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.application.v2.ApplicationService/CreateApplication", map[string]any{
		"projectId": project.ProjectId,
		"name":      "timadorus-web",
		"oidcConfiguration": map[string]any{
			"redirectUris":           []string{spaRedirectURI},
			"postLogoutRedirectUris": []string{spaPostLogoutURI},
			"responseTypes":          []string{"OIDC_RESPONSE_TYPE_CODE"},
			"grantTypes":             []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
			"appType":                "OIDC_APP_TYPE_USER_AGENT",
			"authMethodType":         "OIDC_AUTH_METHOD_TYPE_NONE",
			"version":                "OIDC_VERSION_1_0",
		},
	}, &spaApp); err != nil {
		return "", "", "", fmt.Errorf("create SPA application: %w", err)
	}

	// CORRECTION (evidence-based, design-level, not just a field-name typo — see
	// task-4-report.md): the brief's plan for machine-to-machine access was a second
	// Project Application (apiConfiguration, authMethodType API_AUTH_METHOD_TYPE_BASIC),
	// using its clientId/clientSecret against "POST /oauth/v2/token" with
	// grant_type=client_credentials. Live, that returns a real HTTP 200 with a genuine
	// clientId/clientSecret pair from CreateApplication, but the token endpoint itself then
	// rejects those exact credentials with "HTTP 400: {\"error\":\"invalid_client\",
	// \"error_description\":\"client not found\"}" — reproduced identically against BOTH an
	// "apiConfiguration" Application AND an OIDC-type confidential Application with
	// grantTypes explicitly including "OIDC_GRANT_TYPE_CLIENT_CREDENTIALS". ZITADEL's own
	// docs (https://zitadel.com/docs/guides/integrate/service-accounts/client-credentials)
	// confirm why: the client_credentials grant is implemented against Machine (service)
	// Users, not Project Applications at all — "the client id is the users username" — so no
	// Application-shaped clientId is ever a valid client_credentials client, regardless of
	// its config. Fix: create a dedicated Machine User (CreateUser) and generate a secret for
	// it (AddSecret) instead of a second CreateApplication call. Verified live end-to-end:
	// "POST /oauth/v2/token" with the resulting username/secret returns a real access_token
	// (see task-4-report.md Step 3).
	apiUsername := "timadorus-dev-curl"
	var machineUser struct {
		Id string `json:"id"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.user.v2.UserService/CreateUser", map[string]any{
		"organizationId": organizationId,
		"username":       apiUsername,
		"machine": map[string]any{
			"name": "devcluster API client (client_credentials)",
		},
	}, &machineUser); err != nil {
		return "", "", "", fmt.Errorf("create API machine user: %w", err)
	}

	// AddSecret's response carries only "clientSecret" — no clientId field. Per ZITADEL's own
	// docs, the client_id for this machine user's client_credentials grant is always its
	// username, not its (numeric) user ID.
	var secret struct {
		ClientSecret string `json:"clientSecret"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.user.v2.UserService/AddSecret",
		map[string]string{"userId": machineUser.Id}, &secret); err != nil {
		return "", "", "", fmt.Errorf("generate API client secret: %w", err)
	}

	return spaApp.OidcConfiguration.ClientId, apiUsername, secret.ClientSecret, nil
}

// zitadelBootstrapSecretName is a Kubernetes Secret in ZitadelNamespace that caches the full
// ZitadelBootstrap result as JSON, written once right after InstallZitadel first provisions
// everything. This is what lets a later `up` run — one that finds Zitadel already installed —
// recover the exact same bootstrap values (including the human test user's password, which
// Zitadel itself never exposes again after creation) without needing to re-derive them by
// listing/searching Zitadel's own API for the Project/Applications it already created.
const zitadelBootstrapSecretName = "zitadel-bootstrap"

// saveZitadelBootstrapSecret persists b as a Kubernetes Secret so a later `up` run (Zitadel
// already installed) can recover it via FetchZitadelBootstrap without any Management API
// calls.
func saveZitadelBootstrapSecret(b ZitadelBootstrap) error {
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("e2eutil: marshal zitadel bootstrap: %w", err)
	}
	cmd := exec.Command("kubectl", "create", "secret", "generic", zitadelBootstrapSecretName,
		"--namespace", ZitadelNamespace,
		"--from-literal=bootstrap.json="+string(data),
		"--dry-run=client", "-o", "yaml")
	var applyCmd = exec.Command("kubectl", "apply", "-f", "-")
	manifest, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("e2eutil: render zitadel bootstrap secret manifest: %w", err)
	}
	applyCmd.Stdin = bytes.NewReader(manifest)
	if _, err := Run(applyCmd); err != nil {
		return fmt.Errorf("e2eutil: apply zitadel bootstrap secret: %w", err)
	}
	return nil
}

// FetchZitadelBootstrap reads back the ZitadelBootstrap a prior InstallZitadel call persisted
// (saveZitadelBootstrapSecret) — used when a later `up` run finds Zitadel already installed,
// so it doesn't need to re-provision (or re-derive via the Management API) a Project/
// Applications/test-user that already exist.
func FetchZitadelBootstrap() (ZitadelBootstrap, error) {
	out, err := Run(exec.Command("kubectl", "get", "secret", zitadelBootstrapSecretName,
		"--namespace", ZitadelNamespace,
		"-o", "jsonpath={.data.bootstrap\\.json}"))
	if err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: read zitadel bootstrap secret: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: decode zitadel bootstrap secret: %w", err)
	}
	var b ZitadelBootstrap
	if err := json.Unmarshal(data, &b); err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: unmarshal zitadel bootstrap secret: %w", err)
	}
	return b, nil
}

// InstallZitadel provisions everything devcluster needs: the Helm release (Task 3), then a
// Project with a public/PKCE Application (browser SPA login) and a Machine User with a
// generated secret (client_credentials, scripted/curl access) via the Management API, then
// caches the result
// (saveZitadelBootstrapSecret) so a later run can recover it via FetchZitadelBootstrap without
// re-provisioning. externalPort must match whatever local port devcluster port-forwards
// Zitadel's Service to, and must agree with the same value InstallPlatform's
// PlatformInstallInputs.PathRoutingHostname-derived web base URLs use for its own port (Task
// 5). spaRedirectURI/spaPostLogoutURI are the web SPA's own /login and / routes on the shared
// Traefik-fronted origin (Task 5's port).
func InstallZitadel(externalPort int, spaRedirectURI, spaPostLogoutURI string) (ZitadelBootstrap, error) {
	humanUsername := "devuser"
	humanPassword, err := randomSecret(16)
	if err != nil {
		return ZitadelBootstrap{}, err
	}

	if err := installZitadelHelmRelease(externalPort, humanUsername, humanPassword); err != nil {
		return ZitadelBootstrap{}, err
	}

	pat, err := readZitadelPAT()
	if err != nil {
		return ZitadelBootstrap{}, err
	}

	authority := fmt.Sprintf("http://localhost:%d", externalPort)
	spaClientID, apiClientID, apiClientSecret, err := bootstrapZitadelProject(authority, pat, spaRedirectURI, spaPostLogoutURI)
	if err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: bootstrap zitadel project: %w", err)
	}

	b := ZitadelBootstrap{
		Authority:       authority,
		JWKSURL:         authority + "/oauth/v2/keys",
		SPAClientID:     spaClientID,
		APIClientID:     apiClientID,
		APIClientSecret: apiClientSecret,
		TestUsername:    humanUsername,
		TestPassword:    humanPassword,
	}
	if err := saveZitadelBootstrapSecret(b); err != nil {
		return ZitadelBootstrap{}, err
	}
	return b, nil
}
