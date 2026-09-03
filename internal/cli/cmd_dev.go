package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/devmgr"
	"oroborus.dev/orotools/internal/ui"
)

func newDevCmd() *cobra.Command {
	dev := &cobra.Command{
		Use:   "dev",
		Short: "Gerencia projetos de clientes (list/status/start/stop/logs/open/add)",
		Long:  "Gerencia os dev servers de projetos de clientes a partir do projects.config.json ($CLIENTES_HOME).\nSubstitui o Clientes Dev Manager (dev-manager.sh) com o mesmo schema — migração zero.",
		Args:  cobra.ArbitraryArgs,
		// Falha de start/boot é resultado operacional (reportado em estilo),
		// não erro de uso — mesmo padrão do doctor.
		SilenceUsage: true,
		// `oro dev <atalho>` fallback → start (FR-12)
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runDevStart(cmd, args)
		},
	}
	dev.PersistentFlags().String("config", "", "caminho para projects.config.json (sobrepõe $CLIENTES_HOME)")
	dev.PersistentFlags().String("clients-home", "", "diretório base dos projetos de clientes (padrão $CLIENTES_HOME)")
	dev.PersistentFlags().Duration("wait", 30*time.Second, "tempo máximo aguardando o serviço ficar pronto após o start")
	dev.PersistentFlags().Bool("no-wait", false, "não aguardar prontidão pós-start (comportamento legado)")
	dev.AddCommand(newDevListCmd())
	dev.AddCommand(newDevStatusCmd())
	dev.AddCommand(newDevStartCmd())
	dev.AddCommand(newDevStopCmd())
	dev.AddCommand(newDevLogsCmd())
	dev.AddCommand(newDevOpenCmd())
	dev.AddCommand(newDevAddCmd())
	return dev
}

// resolveDevPath resolves the config path from flags then env (FR-3).
func resolveDevPath(cmd *cobra.Command) (string, error) {
	// Persistent flags defined on the parent surface on child commands via
	// cmd.Flags(), not cmd.PersistentFlags(). Read both to be safe.
	flag, _ := cmd.Flags().GetString("config")
	if flag == "" {
		flag, _ = cmd.PersistentFlags().GetString("config")
	}
	homeFlag, _ := cmd.Flags().GetString("clients-home")
	if homeFlag == "" {
		homeFlag, _ = cmd.PersistentFlags().GetString("clients-home")
	}
	home := os.Getenv("CLIENTES_HOME")
	if homeFlag != "" {
		home = homeFlag
	}
	var err error
	if flag, err = cleanPathArg(flag); err != nil {
		return "", err
	}
	if home, err = cleanPathArg(home); err != nil {
		return "", err
	}
	// o override --config é restrito a arquivos .json do projects.config.json
	if flag != "" && filepath.Ext(flag) != ".json" {
		return "", fmt.Errorf("--config deve apontar para um arquivo .json: %q", flag)
	}
	return devmgr.ResolveConfigPath(home, flag)
}

// cleanPathArg sanitizes um caminho vindo de flag/env antes de qualquer uso
// em filesystem: rejeita caracteres de controle e normaliza lexicalmente.
func cleanPathArg(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.ContainsRune(p, '\x00') {
		return "", fmt.Errorf("caminho de config com caractere inválido: %q", p)
	}
	return filepath.Clean(p), nil
}

// loadDev loads the config, resolving path via flags/env.
func loadDev(cmd *cobra.Command) (*devmgr.Config, error) {
	p, err := resolveDevPath(cmd)
	if err != nil {
		return nil, err
	}
	cfg, err := devmgr.Load(p)
	if err != nil {
		return nil, err
	}
	// Resolve relative log/pid dirs against config base dir.
	if cfg.Settings.PidDir == "" && cfg.Settings.LogDir == "" {
		dir := filepath.Dir(p)
		cfg.Settings.PidDir = filepath.Join(dir, ".dev-pids")
		cfg.Settings.LogDir = filepath.Join(dir, ".dev-logs")
	}
	return cfg, nil
}

func devPidDir(cfg *devmgr.Config) string { return cfg.Settings.PidDir }
func devLogDir(cfg *devmgr.Config) string { return cfg.Settings.LogDir }

// devKeyValid rejeita atalhos que poderiam escapar do diretório de logs/pids
// via path traversal.
func devKeyValid(key string) bool {
	return key != "" && key != "." && key != ".." &&
		!strings.ContainsAny(key, `/\`) && !strings.ContainsRune(key, '\x00')
}

func devLogPath(cfg *devmgr.Config, key string) (string, error) {
	if !devKeyValid(key) {
		return "", fmt.Errorf("atalho inválido para arquivo de log: %q", key)
	}
	joined := filepath.Join(cfg.Settings.LogDir, key+".log")
	// contenção explícita: o caminho final precisa permanecer dentro de LogDir
	rel, err := filepath.Rel(cfg.Settings.LogDir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("caminho de log escapa do diretório de logs: %q", joined)
	}
	return joined, nil
}

// ---------------------------------------------------------------------------
// oro dev list — tabela lipgloss in-process (FR-18, NFR-1)
// ---------------------------------------------------------------------------

func newDevListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista projetos cadastrados",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadDev(cmd)
			if err != nil {
				return err
			}
			configPath, err := resolveDevPath(cmd)
			if err != nil {
				return err
			}
			writeDevTable(cmd.OutOrStdout(), cfg, filepath.Dir(configPath))
			return nil
		},
	}
}

// projectMarker reports running/port-warn status glyph.
func projectMarker(cfg *devmgr.Config, p *devmgr.Project, key string) string {
	if devmgr.IsRunning(devPidDir(cfg), key) {
		return "▶"
	}
	if p.Port != nil && devmgr.PortInUse(*p.Port) {
		return "!"
	}
	return "○"
}

// asciiMarker degrada o glifo de status para writers não-TTY.
func asciiMarker(m string) string {
	switch m {
	case "▶":
		return "+"
	case "!":
		return "!"
	default:
		return "o"
	}
}

func writeDevTable(w io.Writer, cfg *devmgr.Config, base string) {
	pal := ui.PaletteFor(w)
	tty := ui.IsTTY(w)

	type row struct {
		key, name, pm, port, marker string
	}
	var rows []row
	var markers []string
	for _, k := range cfg.Keys() {
		p, _ := cfg.Lookup(k)
		port := "—"
		if p.Port != nil {
			port = strconv.Itoa(*p.Port)
		}
		m := projectMarker(cfg, p, k)
		if !tty {
			m = asciiMarker(m)
		}
		rows = append(rows, row{k, p.Name, p.PackageManager, port, m})
		markers = append(markers, m)
	}

	// lipgloss/table alinha as colunas por display width (ANSI-aware), o que
	// mantém marcadores multibyte (▶/○) e ASCII (!) na mesma coluna.
	t := table.New().
		Headers("Status", "Atalho", "Nome", "PM", "Porta").
		Border(lipgloss.NormalBorder()).
		BorderStyle(pal.Muted).
		StyleFunc(func(r, c int) lipgloss.Style {
			if r == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Padding(0, 1)
			}
			switch c {
			case 0:
				switch markers[r] {
				case "▶", "+":
					return pal.Success.Padding(0, 1)
				case "!":
					return pal.Warn.Padding(0, 1)
				default:
					return pal.Muted.Padding(0, 1)
				}
			case 1:
				return pal.Key.Padding(0, 1)
			case 4:
				return pal.Muted.Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})
	for _, r := range rows {
		t.Row(r.marker, r.key, r.name, r.pm, r.port)
	}

	run, warn, stop := "▶", "!", "○"
	if !tty {
		run, warn, stop = "+", "!", "o"
	}
	legend := fmt.Sprintf(" %s rodando   %s porta ocupada   %s parado",
		pal.Success.Render(run), pal.Warn.Render(warn), pal.Muted.Render(stop))

	fmt.Fprintln(w, pal.Title.Render("Projetos disponíveis em: "+base))
	fmt.Fprintln(w)
	fmt.Fprintln(w, t.Render())
	fmt.Fprintln(w, legend)
}

// ---------------------------------------------------------------------------
// oro dev status
// ---------------------------------------------------------------------------

func newDevStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Mostra PID, porta e status dos projetos que estão rodando",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadDev(cmd)
			if err != nil {
				return err
			}
			status := ui.NewStatus(cmd.OutOrStdout())
			status.Title("Projetos rodando")
			fmt.Fprintln(cmd.OutOrStdout())
			found := false
			for _, k := range cfg.Keys() {
				if !devmgr.IsRunning(devPidDir(cfg), k) {
					continue
				}
				found = true
				pid, _ := devmgr.ReadPid(devPidDir(cfg), k)
				p, _ := cfg.Lookup(k)
				status.Success(fmt.Sprintf("%s (PID %d)", k, pid))
				if p.Port != nil {
					status.Info(fmt.Sprintf("  http://localhost:%d", *p.Port))
				}
				if logPath, err := devLogPath(cfg, k); err == nil {
					status.Info(fmt.Sprintf("  log: %s", logPath))
				}
			}
			if !found {
				status.Warn("nenhum projeto rodando; use 'oro dev start <atalho>'")
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// oro dev start / `oro dev <atalho>`
// ---------------------------------------------------------------------------

func newDevStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "start <atalho>...",
		Aliases:      []string{"run", "up"},
		Short:        "Inicia um ou mais projetos em background",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE:         runDevStart,
	}
}

// devWaitTimeout resolves the post-start readiness timeout from flags
// (FR-22): --no-wait wins over --wait; a non-positive --wait disables too.
// Persistent flags of the parent may not be merged into cmd.Flags() when the
// command is invoked directly (see resolveDevPath), so fall back explicitly.
func devWaitTimeout(cmd *cobra.Command) time.Duration {
	flags := cmd.Flags()
	if flags.Lookup("no-wait") == nil {
		flags = cmd.PersistentFlags()
	}
	if v, _ := flags.GetBool("no-wait"); v {
		return 0
	}
	if v, _ := flags.GetDuration("wait"); v > 0 {
		return v
	}
	return 0
}

// reportKind classifies a start report line for rendering.
type reportKind int

const (
	reportOK reportKind = iota
	reportWarn
	reportErr
)

func runDevStart(cmd *cobra.Command, keys []string) error {
	cfg, err := loadDev(cmd)
	if err != nil {
		return err
	}
	status := ui.NewStatus(cmd.OutOrStdout())
	wait := devWaitTimeout(cmd)

	// FR-6 pre-start block: validate every port before the parallel starts
	// fire, so batch starts can't race past the check or collide mid-batch.
	if err := devmgr.CheckStartPorts(cfg, keys); err != nil {
		return err
	}

	if wait > 0 {
		starting := 0
		for _, k := range keys {
			if p, ok := cfg.Lookup(k); ok && !devmgr.IsRunning(devPidDir(cfg), k) && p.DevCommand != "" {
				starting++
			}
		}
		if starting > 0 {
			status.Info(fmt.Sprintf("iniciando %d projeto(s); aguardando prontidão (até %s)", starting, wait))
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	type startReport struct {
		msg     string
		kind    reportKind
		details []string
	}
	outs := make([]startReport, 0, len(keys))

	report := func(msg string, kind reportKind, details ...string) {
		mu.Lock()
		outs = append(outs, startReport{msg: msg, kind: kind, details: details})
		mu.Unlock()
	}
	fail := func() {
		mu.Lock()
		failed++
		mu.Unlock()
	}

	for _, k := range keys {
		k := k
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, ok := cfg.Lookup(k)
			if !ok {
				report(fmt.Sprintf("%s não encontrado", k), reportErr)
				fail()
				return
			}
			if devmgr.IsRunning(devPidDir(cfg), k) {
				pid, _ := devmgr.ReadPid(devPidDir(cfg), k)
				report(fmt.Sprintf("%s já está rodando (PID %d)", k, pid), reportOK)
				return
			}
			if err := os.MkdirAll(devLogDir(cfg), 0o755); err != nil {
				report(fmt.Sprintf("%s: %v", k, err), reportErr)
				fail()
				return
			}
			sc := devmgr.BuildStartCommand(p.DevCommand, p.Port, cfg.Settings.EnforceUniquePorts)
			pid, err := devmgr.Start(cfg, k, sc)
			if err != nil {
				report(fmt.Sprintf("%s: %v", k, err), reportErr)
				fail()
				return
			}
			// FR-22: esperar o serviço de fato subir (ou morrer) antes de
			// reportar sucesso — spawn com PID não garante boot.
			if wait == 0 {
				msg := fmt.Sprintf("%s iniciado (PID %d)", k, pid)
				if p.Port != nil {
					msg += fmt.Sprintf(" → http://localhost:%d", *p.Port)
				}
				report(msg, reportOK)
				return
			}
			switch devmgr.WaitReady(devPidDir(cfg), k, p.Port, wait) {
			case devmgr.Ready:
				report(fmt.Sprintf("%s no ar (PID %d) → http://localhost:%d", k, pid, *p.Port), reportOK)
			case devmgr.AliveNoPort:
				report(fmt.Sprintf("%s iniciado (PID %d; sem porta para verificar)", k, pid), reportOK)
			case devmgr.StillStarting:
				report(fmt.Sprintf("%s ainda subindo — porta %d não abriu em %s; acompanhe: oro dev logs %s",
					k, *p.Port, wait, k), reportWarn)
			case devmgr.Died:
				details := []string{}
				if logPath, lerr := devLogPath(cfg, k); lerr == nil {
					details = append(details, fmt.Sprintf("últimas linhas do log (%s):", logPath))
					for _, ln := range lastLines(logPath, 8) {
						details = append(details, "  "+ln)
					}
				}
				details = append(details, fmt.Sprintf("acompanhe com: oro dev logs %s", k))
				report(fmt.Sprintf("%s morreu durante o boot (PID %d)", k, pid), reportErr, details...)
				fail()
			}
		}()
	}
	wg.Wait()

	for _, o := range outs {
		switch o.kind {
		case reportErr:
			status.Error(o.msg)
		case reportWarn:
			status.Warn(o.msg)
		default:
			status.Success(o.msg)
		}
		for _, d := range o.details {
			status.Info(d)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d projeto(s) falharam ao iniciar", failed)
	}
	return nil
}

// lastLines returns up to n final non-empty lines of path, reading only the
// trailing chunk — the dev log is truncated on each start, so it holds just
// the current boot attempt.
func lastLines(path string, n int) []string {
	const chunk = 4096
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, chunk)
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil
	}
	if end > chunk {
		if _, err := f.Seek(-chunk, io.SeekEnd); err != nil {
			return nil
		}
	} else if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	read, err := f.Read(buf)
	if err != nil && read <= 0 {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(buf[:read]), "\n"), "\n")
	out := make([]string, 0, n)
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// oro dev stop
// ---------------------------------------------------------------------------

func newDevStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "stop <atalho>...",
		Aliases: []string{"down"},
		Short:   "Para um ou mais projetos (kill de árvore, sem --force)",
		Args:    cobra.ArbitraryArgs,
		RunE:    runDevStop,
	}
	cmd.Flags().Bool("all", false, "para todos os projetos")
	return cmd
}

func runDevStop(cmd *cobra.Command, args []string) error {
	cfg, err := loadDev(cmd)
	if err != nil {
		return err
	}
	status := ui.NewStatus(cmd.OutOrStdout())
	all, _ := cmd.Flags().GetBool("all")
	keys := args
	if all {
		keys = cfg.Keys()
	}
	if len(keys) == 0 {
		return errors.New("use 'oro dev stop <atalho>...' ou 'oro dev stop --all'")
	}
	stopped := 0
	for _, k := range keys {
		if !devmgr.IsRunning(devPidDir(cfg), k) {
			status.Info(fmt.Sprintf("%s não está rodando", k))
			continue
		}
		if err := devmgr.Stop(devPidDir(cfg), k); err != nil {
			status.Error(fmt.Sprintf("%s: %v", k, err))
			continue
		}
		stopped++
		status.Success(fmt.Sprintf("%s parado", k))
	}
	if all && stopped == 0 {
		status.Warn("nenhum projeto estava rodando")
	}
	return nil
}

// ---------------------------------------------------------------------------
// oro dev logs (tail in-process)
// ---------------------------------------------------------------------------

func newDevLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <atalho>",
		Short: "Acompanha os logs do projeto (tail em tempo real)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadDev(cmd)
			if err != nil {
				return err
			}
			key := args[0]
			if _, ok := cfg.Lookup(key); !ok {
				return fmt.Errorf("projeto %q não encontrado", key)
			}
			logPath, err := devLogPath(cfg, key)
			if err != nil {
				return err
			}
			return tailFile(cmd.OutOrStdout(), logPath)
		},
	}
}

// tailFile streams a file and follows growth without coreutils (FR-15).
func tailFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer f.Close()
	buf := make([]byte, 4096)
	for {
		n, _ := f.Read(buf)
		if n > 0 {
			fmt.Fprint(w, string(buf[:n]))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// oro dev open
// ---------------------------------------------------------------------------

func newDevOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <atalho>",
		Short: "Abre o navegador na porta do projeto (xdg-open)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadDev(cmd)
			if err != nil {
				return err
			}
			key := args[0]
			p, ok := cfg.Lookup(key)
			if !ok {
				return fmt.Errorf("projeto %q não encontrado", key)
			}
			if p.Port == nil {
				return fmt.Errorf("projeto %q sem porta configurada", key)
			}
			url := fmt.Sprintf("http://localhost:%d", *p.Port)
			status := ui.NewStatus(cmd.OutOrStdout())
			status.Info("abrindo " + url)
			return openInBrowser(url)
		},
	}
}

func openInBrowser(url string) error {
	// `oro dev open` só constrói URLs http de loopback; recusar qualquer
	// outra forma antes de entregar a um programa externo.
	if !strings.HasPrefix(url, "http://localhost:") && !strings.HasPrefix(url, "http://127.0.0.1:") {
		return fmt.Errorf("URL recusada (esperado http://localhost:<porta>): %s", url)
	}
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command("xdg-open", url).Start()
	}
	if _, err := exec.LookPath("open"); err == nil {
		return exec.Command("open", url).Start()
	}
	return fmt.Errorf("xdg-open/open não encontrado; acesse %s manualmente", url)
}

//
// ---------------------------------------------------------------------------
// oro dev add (wizard huh com validação de porta única)
// ---------------------------------------------------------------------------

func newDevAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Cadastra novo projeto (wizard com próxima porta livre sugerida)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadDev(cmd)
			if err != nil {
				return err
			}
			configPath, err := resolveDevPath(cmd)
			if err != nil {
				return err
			}
			return runDevAdd(cmd, cfg, configPath)
		},
	}
}

var errWizardCancel = errors.New("cancelado pelo usuário")

func runDevAdd(cmd *cobra.Command, cfg *devmgr.Config, configPath string) error {
	status := ui.NewStatus(cmd.OutOrStdout())

	var key, name, path, workDir, pm, devCmd, desc string

	design := []huh.Field{
		huh.NewInput().Title("Atalho curto (ex: meuprojeto)").Value(&key),
		huh.NewInput().Title("Nome exibido").Value(&name),
		huh.NewInput().Title("Caminho raiz (path)").Value(&path).Placeholder(os.Getenv("CLIENTES_HOME") + "/<atalho>"),
		huh.NewInput().Title("Diretório de execução (cwd) [ENTER = path]").Value(&workDir),
		huh.NewSelect[string]().Title("Package manager").Options(
			huh.NewOption("npm", "npm"),
			huh.NewOption("pnpm", "pnpm"),
		).Value(&pm),
		huh.NewInput().Title("Comando dev").Value(&devCmd),
		huh.NewInput().Title("Descrição curta").Value(&desc),
	}
	if err := huh.NewForm(huh.NewGroup(design...)).Run(); err != nil {
		return errWizardCancel
	}
	if key == "" {
		return errors.New("atalho obrigatório")
	}
	if path == "" {
		path = filepath.Join(os.Getenv("CLIENTES_HOME"), key)
	}
	if workDir == "" {
		workDir = path
	}
	if pm == "" {
		pm = "npm"
	}
	if devCmd == "" {
		devCmd = fmt.Sprintf("%s run dev", pm)
	}

	// Sem sobrescrita silenciosa: um atalho já registrado é rejeitado.
	if _, ok := cfg.Lookup(key); ok {
		return fmt.Errorf("%w: atalho %q já existe", devmgr.ErrProjectExists, key)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return fmt.Errorf("caminho inexistente ou não é diretório: %q", path)
	}

	status.Info("validando...")
	project := &devmgr.Project{
		Name:           name,
		Path:           path,
		Cwd:            workDir,
		PackageManager: pm,
		DevCommand:     devCmd,
		Description:    desc,
	}

	// Sugestão de porta só com o comando dev já conhecido (FR-17).
	assigned := assignedPortsFrom(cfg)
	portDefault := ""
	if next, ok := devmgr.NextFreePort(devmgr.DevRange(devCmd), assigned); ok && next > 0 {
		portDefault = strconv.Itoa(next)
	}
	var portStr string
	portField := huh.NewInput().Title("Porta (sugerida: " + portDefault + ")").Value(&portStr).Placeholder("sem porta")
	if err := huh.NewForm(huh.NewGroup(portField)).Run(); err != nil {
		return errWizardCancel
	}
	if portStr != "" {
		n, perr := strconv.Atoi(portStr)
		if perr != nil || n <= 0 || n > 65535 {
			return fmt.Errorf("porta inválida: %q (deve estar entre 1 e 65535)", portStr)
		}
		if err := devmgr.ValidateUniquePorts(cfg, key, n); err != nil {
			return err
		}
		project.Port = &n
	}
	cfg.Add(key, project)
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("salvar config: %w", err)
	}
	status.Success(fmt.Sprintf("projeto %q adicionado", key))
	return nil
}

// ---------------------------------------------------------------------------

// assignedPortsFrom builds the port→slug map for wizard suggestions.
func assignedPortsFrom(cfg *devmgr.Config) map[int]string {
	return devmgr.AssignedPorts(cfg)
}