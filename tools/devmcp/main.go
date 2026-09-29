// Command devmcp 是 exchange 项目的本地开发 MCP Server。
//
// 它通过本地 `ssh exchange`（~/.ssh/config 已配置 ProxyJump 跳板机）把命令投递到
// 测试服务器，并封装 docker compose、PostgreSQL、Redis、ClickHouse 的常用操作。
// 数据库凭据全部来自测试服务器上的 /opt/exchange/infra/.env，本地不保存任何密码。
//
// 自检：go run . --check
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	sshHost  = envOr("DEVMCP_SSH_HOST", "exchange")
	infraDir = envOr("DEVMCP_INFRA_DIR", "/opt/exchange/infra")
)

const (
	defaultTimeout = 60 * time.Second
	maxTimeout     = 600 * time.Second
	maxOutputBytes = 60_000
)

var (
	safeName   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	safeMode   = regexp.MustCompile(`^[0-7]{3,4}$`)
	safeFormat = regexp.MustCompile(`^[A-Za-z]+$`)
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// q 做 POSIX 单引号转义，结果可以安全地嵌入 bash 命令行。
func q(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// withEnv 生成远端前置脚本：进入 infra 目录并加载 .env 里的凭据。
func withEnv(rest string) string {
	return "cd " + q(infraDir) + " && set -a && . ./.env && set +a && " + rest
}

type remoteResult struct {
	Output   string
	ExitCode int
}

// runRemote 在测试服务器上用 bash -c 执行 script。
// 远端 sshd 会把命令交给登录 shell 解析一次，所以这里做一层单引号转义。
func runRemote(ctx context.Context, script string, stdin io.Reader, timeout time.Duration) (remoteResult, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	remoteCmd := "bash -c " + q(script)
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", sshHost, remoteCmd)
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = 5 * time.Second

	err := cmd.Run()
	res := remoteResult{Output: out.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		return res, fmt.Errorf("命令超过 %s 未完成，已终止", timeout)
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	default:
		return res, err
	}
}

func truncate(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + fmt.Sprintf("\n...[输出已截断，共 %d 字节，仅显示前 %d 字节]", len(s), maxOutputBytes)
}

func textResult(text string, isError bool) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: isError,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func toolResult(res remoteResult, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		text := err.Error()
		if res.Output != "" {
			text += "\n" + truncate(res.Output)
		}
		return textResult(text, true)
	}
	text := truncate(res.Output)
	if res.ExitCode != 0 {
		return textResult(fmt.Sprintf("exit code %d\n%s", res.ExitCode, text), true)
	}
	if strings.TrimSpace(text) == "" {
		text = "(无输出)"
	}
	return textResult(text, false)
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }

// ---------- remote_exec ----------

type execArgs struct {
	Command    string `json:"command" jsonschema:"要在测试服务器上执行的 shell 命令（通过 bash -c 运行，可多行、可用管道）"`
	Workdir    string `json:"workdir,omitempty" jsonschema:"工作目录，默认 /opt/exchange/infra"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"超时秒数，默认 60，最大 600"`
}

func remoteExec(ctx context.Context, _ *mcp.CallToolRequest, a execArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(a.Command) == "" {
		return textResult("command 不能为空", true)
	}
	dir := a.Workdir
	if dir == "" {
		dir = infraDir
	}
	script := "cd " + q(dir) + " && " + a.Command
	return toolResult(runRemote(ctx, script, nil, seconds(a.TimeoutSec)))
}

// ---------- remote_put_file ----------

type putArgs struct {
	LocalPath  string `json:"local_path" jsonschema:"本地文件的绝对路径"`
	RemotePath string `json:"remote_path" jsonschema:"测试服务器上的目标文件路径，父目录不存在时会自动创建"`
	Mode       string `json:"mode,omitempty" jsonschema:"上传后 chmod 的权限，例如 0755，可选"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"超时秒数，默认 60，最大 600"`
}

func remotePutFile(ctx context.Context, _ *mcp.CallToolRequest, a putArgs) (*mcp.CallToolResult, any, error) {
	if a.Mode != "" && !safeMode.MatchString(a.Mode) {
		return textResult("mode 必须是 3 到 4 位八进制数，例如 0755", true)
	}
	f, err := os.Open(a.LocalPath)
	if err != nil {
		return textResult("打开本地文件失败: "+err.Error(), true)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return textResult("local_path 必须是一个普通文件", true)
	}
	script := "mkdir -p \"$(dirname " + q(a.RemotePath) + ")\" && cat > " + q(a.RemotePath)
	if a.Mode != "" {
		script += " && chmod " + a.Mode + " " + q(a.RemotePath)
	}
	script += " && ls -l " + q(a.RemotePath)
	res, err := runRemote(ctx, script, f, seconds(a.TimeoutSec))
	if err == nil && res.ExitCode == 0 {
		res.Output = fmt.Sprintf("已上传 %d 字节\n%s", info.Size(), res.Output)
	}
	return toolResult(res, err)
}

// ---------- compose ----------

type composeArgs struct {
	Action     string `json:"action" jsonschema:"操作：ps | logs | up | restart | stop | pull | config"`
	Service    string `json:"service,omitempty" jsonschema:"服务名，可选：postgres | redis | redpanda | clickhouse，留空表示全部"`
	Tail       int    `json:"tail,omitempty" jsonschema:"logs 时返回最近多少行，默认 100"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"超时秒数，默认 60，最大 600"`
}

func compose(ctx context.Context, _ *mcp.CallToolRequest, a composeArgs) (*mcp.CallToolResult, any, error) {
	if a.Service != "" && !safeName.MatchString(a.Service) {
		return textResult("service 名称不合法", true)
	}
	var args []string
	switch a.Action {
	case "ps":
		args = []string{"ps"}
	case "logs":
		tail := a.Tail
		if tail <= 0 {
			tail = 100
		}
		args = []string{"logs", "--no-color", "--tail", strconv.Itoa(tail)}
	case "up":
		args = []string{"up", "-d"}
	case "restart", "stop", "pull":
		args = []string{a.Action}
	case "config":
		args = []string{"config"}
	default:
		return textResult("action 只支持 ps | logs | up | restart | stop | pull | config", true)
	}
	if a.Service != "" {
		args = append(args, a.Service)
	}
	script := "cd " + q(infraDir) + " && sudo docker compose " + strings.Join(args, " ")
	return toolResult(runRemote(ctx, script, nil, seconds(a.TimeoutSec)))
}

// ---------- pg_query ----------

type sqlArgs struct {
	SQL        string `json:"sql" jsonschema:"要执行的 SQL，可多条，用分号分隔；也支持 psql 元命令，例如 \\dt、\\d users"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"超时秒数，默认 60，最大 600"`
}

func pgQuery(ctx context.Context, _ *mcp.CallToolRequest, a sqlArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(a.SQL) == "" {
		return textResult("sql 不能为空", true)
	}
	// SQL 通过 stdin 传入，这样多条语句和 \dt、\d 之类的元命令都能混用。
	script := withEnv(`sudo docker compose exec -T -e PGPASSWORD="$POSTGRES_PASSWORD" postgres ` +
		`psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -P pager=off`)
	return toolResult(runRemote(ctx, script, strings.NewReader(a.SQL+"\n"), seconds(a.TimeoutSec)))
}

// ---------- redis_cmd ----------

type redisArgs struct {
	Command    string `json:"command" jsonschema:"redis-cli 命令，例如 KEYS user:* 或 HGETALL session:1，多个参数用空格分隔，含空格的参数用引号包起来"`
	DB         int    `json:"db,omitempty" jsonschema:"数据库编号，默认 0"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"超时秒数，默认 60，最大 600"`
}

// splitArgs 按空白拆分命令行，支持单引号和双引号包裹的参数。
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("引号未闭合")
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}

func redisCmd(ctx context.Context, _ *mcp.CallToolRequest, a redisArgs) (*mcp.CallToolResult, any, error) {
	parts, err := splitArgs(a.Command)
	if err != nil {
		return textResult(err.Error(), true)
	}
	if len(parts) == 0 {
		return textResult("command 不能为空", true)
	}
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = q(p)
	}
	script := withEnv(`sudo docker compose exec -T redis redis-cli --no-auth-warning -a "$REDIS_PASSWORD" -n ` +
		strconv.Itoa(a.DB) + " " + strings.Join(quoted, " "))
	return toolResult(runRemote(ctx, script, nil, seconds(a.TimeoutSec)))
}

// ---------- ch_query ----------

type chArgs struct {
	SQL        string `json:"sql" jsonschema:"要执行的 ClickHouse SQL，单条语句"`
	Format     string `json:"format,omitempty" jsonschema:"输出格式，默认 PrettyCompact，也可以用 JSON、CSV、TSV、Vertical 等"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"超时秒数，默认 60，最大 600"`
}

func chQuery(ctx context.Context, _ *mcp.CallToolRequest, a chArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(a.SQL) == "" {
		return textResult("sql 不能为空", true)
	}
	format := a.Format
	if format == "" {
		format = "PrettyCompact"
	}
	if !safeFormat.MatchString(format) {
		return textResult("format 只能是字母，例如 PrettyCompact、JSON、CSV", true)
	}
	script := withEnv(`sudo docker compose exec -T clickhouse clickhouse-client ` +
		`--user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --database "$CLICKHOUSE_DB" ` +
		"--format " + format + " --query " + q(a.SQL))
	return toolResult(runRemote(ctx, script, nil, seconds(a.TimeoutSec)))
}

// ---------- main ----------

func main() {
	log.SetFlags(0)
	log.SetPrefix("devmcp: ")

	if len(os.Args) > 1 && os.Args[1] == "--check" {
		res, err := runRemote(context.Background(),
			"echo host=$(hostname) user=$(whoami); cd "+q(infraDir)+" && sudo docker compose ps --format 'table {{.Service}}\t{{.Status}}'",
			nil, 0)
		fmt.Print(res.Output)
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(res.ExitCode)
	}

	const where = "测试服务器（本地 ssh exchange，公网 IP 以环境配置.md 为准，ubuntu 用户，免密 sudo，基础设施在 /opt/exchange/infra 用 docker compose 运行）"

	server := mcp.NewServer(&mcp.Implementation{Name: "exchange-dev", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "remote_exec",
		Description: "在" + where + "上执行任意 shell 命令并返回 stdout+stderr。适合查看文件、系统状态、部署二进制后启动服务等。",
	}, remoteExec)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "remote_put_file",
		Description: "把本地文件上传到" + where + "。用于部署编译好的二进制、配置文件或迁移脚本。",
	}, remotePutFile)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "compose",
		Description: "对" + where + "上的 docker compose 基础设施（postgres、redis、redpanda、clickhouse）执行 ps/logs/up/restart/stop/pull/config。",
	}, compose)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "pg_query",
		Description: "在测试环境 PostgreSQL（数据库 exchange）里执行 SQL，通过容器内 psql 运行，凭据取自服务器上的 .env。可用于查表结构、查数据、执行迁移。",
	}, pgQuery)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "redis_cmd",
		Description: "在测试环境 Redis 里执行一条 redis-cli 命令，例如 KEYS、GET、HGETALL、TTL、INFO。",
	}, redisCmd)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ch_query",
		Description: "在测试环境 ClickHouse（数据库 exchange）里执行一条 SQL，通过容器内 clickhouse-client 运行。",
	}, chQuery)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
