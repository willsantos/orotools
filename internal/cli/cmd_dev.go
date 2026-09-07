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

// loadDev loads the config, resolving path via flags/env. Pid/log dirs come
// anchored by devmgr.Load — independent of the invocation CWD (FR-1).
func loadDev(cmd *cobra.Command) (*devmgr.Config, error) {
	p, err := resolveDevPath(cmd)
	if err != nil {
		return nil, err
	}
	return devmgr.Load(p)
}

func devPidDir(cfg *devmgr.Config) string { return cfg.PidDir() }
func devLogDir(cfg *devmgr.Config) string { return cfg.LogDir() }

// devLogPath devolve o caminho do log delegando a validação de atalho ao
// pacote que de fato escreve/lê os arquivos (FR-8).
func devLogPath(cfg *devmgr.Config, key string) (string, error) {
	return devmgr.LogFile(cfg.LogDir(), key)
}

// ---------------------------------------------------------------------------
// oro dev list — tabela lipgloss in-process (FR-18, NFR-1)
// ---------------------------------------------------------------------------

func newDevListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [grupo...]",
		Short: "Lista projetos cadastrados (opcionalmente filtrando por grupo)",
		// Grupo inexistente é resultado operacional reportado na mensagem —
		// mesmo padrão do comando dev pai; usage poluiria a saída.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDevList(cmd, args)
		},
	}
}

// runDevList implements `oro dev list [grupo...]`: sem argumentos lista
// todos os projetos; com argumentos filtra pela união dos grupos casados
// (dev-project-groups FR-6).
func runDevList(cmd *cobra.Command, args []string) error {
	cfg, err := loadDev(cmd)
	if err != nil {
		return err
	}
	configPath, err := resolveDevPath(cmd)
	if err != nil {
		return err
	}
	if err := validateDevGroups(cfg, args); err != nil {
		return err
	}
	writeDevTable(cmd.OutOrStdout(), cfg, filepath.Dir(configPath), args)
	return nil
}

// validateDevGroups rejects filter args that match no group derived from
// the config (FR-7), listing the available ones — matching is GroupKey
// based (FR-4), so case/accent variants of an existing group pass.
func validateDevGroups(cfg *devmgr.Config, args []string) error {
	if len(args) == 0 {
		return nil
	}
	groups := cfg.Groups()
	available := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		available[devmgr.GroupKey(g)] = struct{}{}
	}
	for _, a := range args {
		if _, ok := available[devmgr.GroupKey(a)]; ok {
			continue
		}
		if len(available) == 0 {
			return errors.New(`nenhum projeto tem grupo configurado (campo "group" no projects.config.json)`)
		}
		return fmt.Errorf("grupo %q não encontrado; disponíveis: %s", a, strings.Join(groups, ", "))
	}
	return nil
}

// projectMarker reports the status glyph and the managed pid (FR-3):
// pid > 0 identifies a process started and tracked by oro; an occupied port
// without a pid means the occupant is external (FR-4).
func projectMarker(cfg *devmgr.Config, p *devmgr.Project, key string) (string, int) {
	st := devmgr.CheckProcess(cfg, key)
	if st.Running {
		return "▶", st.PID
	}
	if p.Port != nil && devmgr.PortInUse(*p.Port) {
		return "!", 0
	}
	return "○", 0
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

func writeDevTable(w io.Writer, cfg *devmgr.Config, base string, filter []string) {
	pal := ui.PaletteFor(w)
	tty := ui.IsTTY(w)

	// FR-6: filtro pela união dos grupos casados; matching por GroupKey,
	// mesmo critério da validação de args em validateDevGroups.
	var filterKeys map[string]struct{}
	if len(filter) > 0 {
		filterKeys = make(map[string]struct{}, len(filter))
		for _, a := range filter {
			filterKeys[devmgr.GroupKey(a)] = struct{}{}
		}
	}

	type row struct {
		key, name, group, pm, port, marker, pid string
	}
	var rows []row
	var markers []string
	for _, k := range cfg.Keys() {
		p, _ := cfg.Lookup(k)
		if filterKeys != nil {
			if _, ok := filterKeys[devmgr.GroupKey(p.Group)]; !ok {
				continue
			}
		}
		port := "—"
		if p.Port != nil {
			port = strconv.Itoa(*p.Port)
		}
		m, pid := projectMarker(cfg, p, k)
		if !tty {
			m = asciiMarker(m)
		}
		pidCell := "—"
		if pid > 0 {
			pidCell = strconv.Itoa(pid)
		}
		rows = append(rows, row{k, p.Name, strings.TrimSpace(p.Group), p.PackageManager, port, m, pidCell})
		markers = append(markers, m)
	}

	// FR-8: no modo filtrado o título identifica os grupos na forma gravada
	// (não os argumentos crus do usuário).
	title := "Projetos disponíveis em: " + base
	if len(filterKeys) > 0 {
		var matched []string
		for _, g := range cfg.Groups() {
			if _, ok := filterKeys[devmgr.GroupKey(g)]; ok {
				matched = append(matched, g)
			}
		}
		if len(matched) > 0 {
			label := "grupo"
			if len(matched) > 1 {
				label = "grupos"
			}
			title += fmt.Sprintf(" — %s: %s", label, strings.Join(matched, ", "))
		}
	}

	// FR-9: a coluna Grupo existe somente quando as linhas exibidas têm 2+
	// chaves de grupo distintas (o sem-grupo contribui com a chave "") —
	// config sem grupos e filtro de grupo único a omitem por redundância.
	groupKeys := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		groupKeys[devmgr.GroupKey(r.group)] = struct{}{}
	}
	showGroup := len(groupKeys) >= 2

	// Índices de coluna condicionais (Grupo entra entre Nome e PM; PM segue
	// com o estilo default logo após).
	groupCol, portCol, pidCol := -1, 4, 5
	if showGroup {
		groupCol, portCol, pidCol = 3, 5, 6
	}
	headers := make([]string, 0, 7)
	headers = append(headers, "Status", "Atalho", "Nome")
	if showGroup {
		headers = append(headers, "Grupo")
	}
	headers = append(headers, "PM", "Porta", "PID")

	// lipgloss/table alinha as colunas por display width (ANSI-aware), o que
	// mantém marcadores multibyte (▶/○) e ASCII (!) na mesma coluna.
	t := table.New().
		Headers(headers...).
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
			case groupCol, portCol, pidCol:
				return pal.Muted.Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})
	for _, r := range rows {
		cells := make([]string, 0, 7)
		cells = append(cells, r.marker, r.key, r.name)
		if showGroup {
			groupCell := r.group
			if groupCell == "" {
				groupCell = "—"
			}
			cells = append(cells, groupCell)
		}
		cells = append(cells, r.pm, r.port, r.pid)
		t.Row(cells...)
	}

	run, warn, stop := "▶", "!", "○"
	if !tty {
		run, warn, stop = "+", "!", "o"
	}
	legend := fmt.Sprintf(" %s rodando   %s porta ocupada   %s parado",
		pal.Success.Render(run), pal.Warn.Render(warn), pal.Muted.Render(stop))

	fmt.Fprintln(w, pal.Title.Render(title))
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
			external := false
			for _, k := range cfg.Keys() {
				st := devmgr.CheckProcess(cfg, k) // também coleta pid file stale (FR-5)
				p, _ := cfg.Lookup(k)
				if st.Running {
					found = true
					status.Success(fmt.Sprintf("%s (PID %d)", k, st.PID))
					if p.Port != nil {
						status.Info(fmt.Sprintf("  http://localhost:%d", *p.Port))
					}
					if logPath, err := devLogPath(cfg, k); err == nil {
						status.Info(fmt.Sprintf("  log: %s", logPath))
					}
					continue
				}
				// FR-4: projeto parado com a porta configurada ocupada é
				// ocupante externo — o oro não lembra de processo que não
				// iniciou, mas não deixa a porta ocupada passar em silêncio.
				if warnExternalOccupant(status, cfg, k) {
					external = true
				}
			}
			if !found && !external {
				status.Warn("nenhum projeto rodando; use 'oro dev start <atalho>'")
			}
			return nil
		},
	}
}

// warnExternalOccupant reports whether key's configured port is held by a
// process oro does not manage, warning on the status output when it does.
func warnExternalOccupant(status *ui.Status, cfg *devmgr.Config, key string) bool {
	p, ok := cfg.Lookup(key)
	if !ok || p.Port == nil || !devmgr.PortInUse(*p.Port) {
		return false
	}
	status.Warn(fmt.Sprintf("%s: porta %d ocupada por processo externo — oro não gerencia", key, *p.Port))
	return true
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
			if p, ok := cfg.Lookup(k); ok && p.DevCommand != "" && !devmgr.CheckProcess(cfg, k).Running {
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
			if st := devmgr.CheckProcess(cfg, k); st.Running {
				report(fmt.Sprintf("%s já está rodando (PID %d)", k, st.PID), reportOK)
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
		switch st := devmgr.Stop(cfg, k); st {
		case devmgr.Stopped:
			stopped++
			status.Success(fmt.Sprintf("%s parado", k))
		case devmgr.StaleForeign:
			status.Warn(fmt.Sprintf("%s: pid file apontava para processo alheio (coletado); nada foi sinalizado", k))
		default: // NotRunning
			if warnExternalOccupant(status, cfg, k) {
				continue
			}
			status.Info(fmt.Sprintf("%s não está rodando", k))
		}
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

// devGroupPlaceholder builds the "Grupo" step suggestion: existing groups
// when there are any, otherwise a hint that empty means ungrouped.
func devGroupPlaceholder(cfg *devmgr.Config) string {
	if gs := cfg.Groups(); len(gs) > 0 {
		return strings.Join(gs, ", ")
	}
	return "sem grupo"
}

func runDevAdd(cmd *cobra.Command, cfg *devmgr.Config, configPath string) error {
	status := ui.NewStatus(cmd.OutOrStdout())

	var key, name, path, workDir, pm, devCmd, desc, group string

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
		// dev-project-groups FR-11: grupo opcional, free-form — grupo novo
		// nasce no primeiro uso; ENTER vazio = sem grupo.
		huh.NewInput().Title("Grupo (opcional)").Value(&group).Placeholder(devGroupPlaceholder(cfg)),
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
	group = strings.TrimSpace(group)

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
		Group:          group,
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
