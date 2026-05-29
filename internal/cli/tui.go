package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type tuiTab int

const (
	tuiMachines tuiTab = iota
	tuiProjects
	tuiRoutes
	tuiShares
	tuiSetup
	tuiConfig
)

var tuiTabs = []string{"Machines", "Projects", "Routes", "Shares", "Setup", "Config"}

type tuiMode int

const (
	tuiModeNormal tuiMode = iota
	tuiModeTenantPicker
	tuiModeProjectPicker
	tuiModeActionPicker
	tuiModeInput
	tuiModeConfirm
	tuiModeOutput
)

type tuiData struct {
	Config          scconfig.Admin
	ConfigPath      string
	IncusConfigPath string
	Tenants         []tenant.Summary
	Tenant          tenant.Summary
	HasTenant       bool
	Machines        []meta.Machine
	Routes          []route.Route
	LoadError       string
	RouteError      string
}

type tuiLoadedMsg struct {
	data tuiData
	err  error
}

type tuiCommandMsg struct {
	args   []string
	output string
	err    error
}

type tuiExternalCommandMsg struct {
	args []string
	err  error
}

type tuiInputHandler func(string) tea.Cmd

type tuiModel struct {
	ctx context.Context

	config commandConfig
	data   tuiData

	tab     tuiTab
	mode    tuiMode
	cursor  int
	actions []tuiAction

	input        textinput.Model
	inputTitle   string
	inputHelp    string
	inputHandler tuiInputHandler

	confirmTitle string
	confirmHelp  string
	confirmCmd   tea.Cmd

	outputTitle string
	output      string

	width  int
	height int
}

type tuiAction struct {
	Label string
	Help  string
	Run   func(*tuiModel) tea.Cmd
}

var (
	tuiTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	tuiMutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	tuiErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	tuiSuccessStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	tuiSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	tuiBoxStyle      = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
)

func runInteractiveHome(ctx context.Context, config commandConfig) error {
	model := newTUIModel(ctx, config)
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newTUIModel(ctx context.Context, config commandConfig) tuiModel {
	input := textinput.New()
	input.Prompt = "> "
	input.CharLimit = 512
	return tuiModel{
		ctx:    ctx,
		config: config,
		tab:    tuiMachines,
		mode:   tuiModeNormal,
		input:  input,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return m.load()
}

func (m tuiModel) load() tea.Cmd {
	config := m.config
	return func() tea.Msg {
		data, err := loadTUIData(m.ctx, config)
		return tuiLoadedMsg{data: data, err: err}
	}
}

func loadTUIData(ctx context.Context, config commandConfig) (tuiData, error) {
	data := tuiData{
		Config:          config.adminConfig,
		ConfigPath:      scconfig.DefaultConfigPath(),
		IncusConfigPath: scconfig.ResolveConfigPath(config.adminConfig.Remote),
	}
	tenants, err := listTenants(ctx, config.tenantStore)
	if err != nil {
		data.LoadError = err.Error()
		return data, nil
	}
	data.Tenants = tenants
	currentTenant := strings.TrimSpace(config.adminConfig.Tenant)
	for _, candidate := range tenants {
		if candidate.Tenant == currentTenant {
			data.Tenant = candidate
			data.HasTenant = true
			break
		}
	}
	if currentTenant == "" {
		data.LoadError = "Current Tenant is not selected"
		return data, nil
	}
	if !data.HasTenant {
		data.LoadError = fmt.Sprintf("Current Tenant %q was not found", currentTenant)
		return data, nil
	}
	list, err := listMachines(ctx, config, listMachinesRequest{AllProjects: true})
	if err != nil {
		data.LoadError = err.Error()
		return data, nil
	}
	data.Machines = list.Machines
	if config.routes != nil {
		plan, err := route.PlanList(config.adminConfig)
		if err != nil {
			data.RouteError = err.Error()
		} else {
			result, err := config.routes.List(ctx, plan)
			if err != nil {
				data.RouteError = err.Error()
			} else {
				data.Routes = result.Routes
			}
		}
	} else {
		data.RouteError = "Route Broker is not configured"
	}
	return data, nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tuiLoadedMsg:
		if msg.err != nil {
			m.data.LoadError = msg.err.Error()
		} else {
			m.data = msg.data
			m.config.adminConfig = msg.data.Config
		}
		m.clampCursor()
		return m, nil
	case tuiCommandMsg:
		m.config.adminConfig = scconfig.LoadUser()
		title := strings.Join(msg.args, " ")
		if title == "" {
			title = "command"
		}
		m.outputTitle = title
		m.output = strings.TrimSpace(msg.output)
		if msg.err != nil {
			if m.output != "" {
				m.output += "\n"
			}
			m.output += "Error: " + msg.err.Error()
		}
		if m.output == "" {
			m.output = "Done."
		}
		m.mode = tuiModeOutput
		return m, m.load()
	case tuiExternalCommandMsg:
		m.outputTitle = strings.Join(msg.args, " ")
		if msg.err != nil {
			m.output = "Error: " + msg.err.Error()
		} else {
			m.output = "Done."
		}
		m.mode = tuiModeOutput
		return m, m.load()
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m tuiModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == tuiModeInput {
		switch msg.String() {
		case "esc":
			m.mode = tuiModeNormal
			return m, nil
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			handler := m.inputHandler
			m.mode = tuiModeNormal
			if handler == nil {
				return m, nil
			}
			return m, handler(value)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "ctrl+c", "q":
		if m.mode == tuiModeNormal {
			return m, tea.Quit
		}
		m.mode = tuiModeNormal
		return m, nil
	case "esc":
		m.mode = tuiModeNormal
		return m, nil
	}
	switch m.mode {
	case tuiModeTenantPicker:
		return m.updateTenantPicker(msg)
	case tuiModeProjectPicker:
		return m.updateProjectPicker(msg)
	case tuiModeActionPicker:
		return m.updateActionPicker(msg)
	case tuiModeConfirm:
		return m.updateConfirm(msg)
	case tuiModeOutput:
		return m.updateOutput(msg)
	}
	return m.updateNormal(msg)
}

func (m tuiModel) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % tuiTab(len(tuiTabs))
		m.cursor = 0
	case "shift+tab", "left", "h":
		m.tab = (m.tab + tuiTab(len(tuiTabs)) - 1) % tuiTab(len(tuiTabs))
		m.cursor = 0
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < m.currentListLen()-1 {
			m.cursor++
		}
	case "r":
		return m, m.load()
	case "t":
		m.mode = tuiModeTenantPicker
		m.cursor = selectedTenantIndex(m.data.Tenants, m.config.adminConfig.Tenant)
	case "p":
		m.mode = tuiModeProjectPicker
		m.cursor = selectedProjectIndex(m.data.Tenant.Projects, m.config.adminConfig.Project)
	case "a", "enter":
		m.actions = m.currentActions()
		m.mode = tuiModeActionPicker
		m.cursor = 0
	case ":":
		return m, m.prompt("Run sc command", "Enter args after sc, for example: create codex --detach", "", func(value string) tea.Cmd {
			if value == "" {
				return nil
			}
			return m.runCommand(strings.Fields(value))
		})
	}
	return m, nil
}

func (m tuiModel) updateTenantPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.data.Tenants)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.data.Tenants) == 0 {
			return m, nil
		}
		name := m.data.Tenants[m.cursor].Tenant
		m.mode = tuiModeNormal
		return m, m.switchTenant(name)
	}
	return m, nil
}

func (m tuiModel) updateProjectPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	projects := m.data.Tenant.Projects
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(projects)-1 {
			m.cursor++
		}
	case "enter":
		if len(projects) == 0 {
			return m, nil
		}
		name := projects[m.cursor].Name
		m.mode = tuiModeNormal
		return m, m.switchProject(name)
	}
	return m, nil
}

func (m tuiModel) updateActionPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.actions)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.actions) == 0 {
			return m, nil
		}
		action := m.actions[m.cursor]
		m.mode = tuiModeNormal
		return m, action.Run(&m)
	}
	return m, nil
}

func (m tuiModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		cmd := m.confirmCmd
		m.mode = tuiModeNormal
		return m, cmd
	case "n", "N":
		m.mode = tuiModeNormal
		return m, nil
	}
	return m, nil
}

func (m tuiModel) updateOutput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		m.mode = tuiModeNormal
		return m, m.load()
	default:
		m.mode = tuiModeNormal
	}
	return m, nil
}

func (m tuiModel) View() string {
	var b strings.Builder
	b.WriteString(tuiTitleStyle.Render("Sandcastle"))
	b.WriteString("\n")
	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")
	switch m.mode {
	case tuiModeTenantPicker:
		b.WriteString(m.renderTenantPicker())
	case tuiModeProjectPicker:
		b.WriteString(m.renderProjectPicker())
	case tuiModeActionPicker:
		b.WriteString(m.renderActionPicker())
	case tuiModeInput:
		b.WriteString(m.renderInput())
	case tuiModeConfirm:
		b.WriteString(m.renderConfirm())
	case tuiModeOutput:
		b.WriteString(m.renderOutput())
	default:
		b.WriteString(m.renderTabs())
		b.WriteString("\n\n")
		b.WriteString(m.renderCurrentTab())
	}
	b.WriteString("\n\n")
	b.WriteString(tuiMutedStyle.Render("tab switch view  t tenant  p project  a actions  : command  r refresh  q quit"))
	return b.String()
}

func (m tuiModel) renderHeader() string {
	tenantName := displayValue(m.config.adminConfig.Tenant)
	projectName := displayValue(m.config.adminConfig.Project)
	if strings.TrimSpace(m.config.adminConfig.Project) == "" {
		projectName = "default"
	}
	ready := tuiSuccessStyle.Render("ready")
	if m.data.LoadError != "" {
		ready = tuiErrorStyle.Render("needs setup")
	}
	lines := []string{
		fmt.Sprintf("Remote: %s   Tenant: %s   Project: %s   %s", displayValue(m.config.adminConfig.Remote), tenantName, projectName, ready),
		fmt.Sprintf("Auth: %s   Config: %s", displayValue(commandAuthHostname(m.config, "")), m.data.ConfigPath),
	}
	if m.data.LoadError != "" {
		lines = append(lines, tuiErrorStyle.Render(m.data.LoadError))
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderTabs() string {
	parts := make([]string, 0, len(tuiTabs))
	for i, tab := range tuiTabs {
		if tuiTab(i) == m.tab {
			parts = append(parts, tuiSelectedStyle.Render(tab))
		} else {
			parts = append(parts, tuiMutedStyle.Render(tab))
		}
	}
	return strings.Join(parts, "  ")
}

func (m tuiModel) renderCurrentTab() string {
	switch m.tab {
	case tuiMachines:
		return m.renderMachines()
	case tuiProjects:
		return m.renderProjects()
	case tuiRoutes:
		return m.renderRoutes()
	case tuiShares:
		return m.renderShares()
	case tuiSetup:
		return m.renderSetup()
	case tuiConfig:
		return m.renderConfig()
	default:
		return ""
	}
}

func (m tuiModel) renderMachines() string {
	if len(m.data.Machines) == 0 {
		return tuiBoxStyle.Render("No Machines found.\nPress a to create one.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-14s %-20s %-36s %-15s %s\n", "Project", "Machine", "FQDN", "IP", "State")
	for i, machine := range m.data.Machines {
		state := "stopped"
		if machine.Running {
			state = "running"
		}
		line := fmt.Sprintf("%-14s %-20s %-36s %-15s %s", machine.Project, machine.Name, machineFQDN(m.data.Tenant, machine), displayValue(machine.PrivateIP), state)
		if i == m.cursor {
			line = tuiSelectedStyle.Render("> " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderProjects() string {
	if len(m.data.Tenant.Projects) == 0 {
		return tuiBoxStyle.Render("No Projects found.")
	}
	var b strings.Builder
	for i, project := range m.data.Tenant.Projects {
		label := project.Name
		if project.Name == currentProjectName(m.config.adminConfig.Project) {
			label += "  current"
		}
		if project.CloudIdentity != "" {
			label += "  cloud:" + project.CloudIdentity
		}
		if project.DockerAutostart {
			label += "  docker-autostart"
		}
		if i == m.cursor {
			label = tuiSelectedStyle.Render("> " + label)
		} else {
			label = "  " + label
		}
		b.WriteString(label + "\n")
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderRoutes() string {
	if m.data.RouteError != "" {
		return tuiBoxStyle.Render(tuiErrorStyle.Render(m.data.RouteError))
	}
	if len(m.data.Routes) == 0 {
		return tuiBoxStyle.Render("No Public Routes found.\nPress a to create one.")
	}
	var b strings.Builder
	for i, publicRoute := range m.data.Routes {
		line := fmt.Sprintf("%s -> %s:%d", publicRoute.Hostname, publicRoute.TargetReference, publicRoute.RoutePort)
		if i == m.cursor {
			line = tuiSelectedStyle.Render("> " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderShares() string {
	actions := m.shareActions()
	var b strings.Builder
	b.WriteString("Tenant Storage Shares\n\n")
	for i, action := range actions {
		item := action.Label
		if action.Help != "" {
			item += "  " + tuiMutedStyle.Render(action.Help)
		}
		if i == m.cursor {
			b.WriteString(tuiSelectedStyle.Render("> "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderSetup() string {
	actions := m.setupActions()
	var b strings.Builder
	for i, action := range actions {
		item := action.Label
		if action.Help != "" {
			item += "  " + tuiMutedStyle.Render(action.Help)
		}
		if i == m.cursor {
			b.WriteString(tuiSelectedStyle.Render("> "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderConfig() string {
	lines := []string{
		"Resolved",
		"  tenant: " + displayValue(m.config.adminConfig.Tenant),
		"  project: " + currentProjectName(m.config.adminConfig.Project),
		"  remote: " + displayValue(m.config.adminConfig.Remote),
		"  auth.hostname: " + displayValue(commandAuthHostname(m.config, "")),
		"",
		"Files",
		"  config: " + m.data.ConfigPath,
		"  incus: " + displayValue(m.data.IncusConfigPath),
		"",
		"Press a to edit config values.",
	}
	return tuiBoxStyle.Render(strings.Join(lines, "\n"))
}

func (m tuiModel) renderTenantPicker() string {
	if len(m.data.Tenants) == 0 {
		return tuiBoxStyle.Render("No Tenants found. Press esc, then use Setup > Login.")
	}
	var b strings.Builder
	b.WriteString("Switch Tenant\n\n")
	for i, tenant := range m.data.Tenants {
		label := tenant.Tenant
		if tenant.Tenant == m.config.adminConfig.Tenant {
			label += "  current"
		}
		if i == m.cursor {
			label = tuiSelectedStyle.Render("> " + label)
		} else {
			label = "  " + label
		}
		b.WriteString(label + "\n")
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderProjectPicker() string {
	if len(m.data.Tenant.Projects) == 0 {
		return tuiBoxStyle.Render("No Projects found.")
	}
	var b strings.Builder
	b.WriteString("Switch Project\n\n")
	for i, project := range m.data.Tenant.Projects {
		label := project.Name
		if project.Name == currentProjectName(m.config.adminConfig.Project) {
			label += "  current"
		}
		if i == m.cursor {
			label = tuiSelectedStyle.Render("> " + label)
		} else {
			label = "  " + label
		}
		b.WriteString(label + "\n")
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderActionPicker() string {
	if len(m.actions) == 0 {
		return tuiBoxStyle.Render("No actions available.")
	}
	var b strings.Builder
	b.WriteString("Actions\n\n")
	for i, action := range m.actions {
		label := action.Label
		if action.Help != "" {
			label += "  " + tuiMutedStyle.Render(action.Help)
		}
		if i == m.cursor {
			label = tuiSelectedStyle.Render("> " + label)
		} else {
			label = "  " + label
		}
		b.WriteString(label + "\n")
	}
	return tuiBoxStyle.Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) renderInput() string {
	lines := []string{m.inputTitle}
	if m.inputHelp != "" {
		lines = append(lines, tuiMutedStyle.Render(m.inputHelp))
	}
	lines = append(lines, "", m.input.View())
	return tuiBoxStyle.Render(strings.Join(lines, "\n"))
}

func (m tuiModel) renderConfirm() string {
	lines := []string{m.confirmTitle}
	if m.confirmHelp != "" {
		lines = append(lines, tuiMutedStyle.Render(m.confirmHelp))
	}
	lines = append(lines, "", "Press y to confirm, n or esc to cancel.")
	return tuiBoxStyle.Render(strings.Join(lines, "\n"))
}

func (m tuiModel) renderOutput() string {
	title := m.outputTitle
	if title == "" {
		title = "Output"
	}
	output := m.output
	if output == "" {
		output = "Done."
	}
	return tuiBoxStyle.Render(title + "\n\n" + output + "\n\n" + tuiMutedStyle.Render("Press any key to return."))
}

func (m *tuiModel) prompt(title, help, value string, handler tuiInputHandler) tea.Cmd {
	m.mode = tuiModeInput
	m.inputTitle = title
	m.inputHelp = help
	m.inputHandler = handler
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.Focus()
	return textinput.Blink
}

func (m *tuiModel) confirm(title, help string, cmd tea.Cmd) tea.Cmd {
	m.mode = tuiModeConfirm
	m.confirmTitle = title
	m.confirmHelp = help
	m.confirmCmd = cmd
	return nil
}

func (m tuiModel) currentListLen() int {
	switch m.tab {
	case tuiMachines:
		return len(m.data.Machines)
	case tuiProjects:
		return len(m.data.Tenant.Projects)
	case tuiRoutes:
		return len(m.data.Routes)
	case tuiShares:
		return len(m.shareActions())
	case tuiSetup:
		return len(m.setupActions())
	default:
		return 0
	}
}

func (m *tuiModel) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if max := m.currentListLen() - 1; max >= 0 && m.cursor > max {
		m.cursor = max
	}
}

func (m tuiModel) currentActions() []tuiAction {
	switch m.tab {
	case tuiMachines:
		return m.machineActions()
	case tuiProjects:
		return m.projectActions()
	case tuiRoutes:
		return m.routeActions()
	case tuiShares:
		return m.shareActions()
	case tuiSetup:
		return m.setupActions()
	case tuiConfig:
		return m.configActions()
	default:
		return nil
	}
}

func (m tuiModel) selectedMachineRef() string {
	if len(m.data.Machines) == 0 || m.cursor >= len(m.data.Machines) {
		return ""
	}
	machine := m.data.Machines[m.cursor]
	if machine.Project != "" {
		return machine.Project + ":" + machine.Name
	}
	return machine.Name
}

func (m tuiModel) selectedProjectName() string {
	if len(m.data.Tenant.Projects) == 0 || m.cursor >= len(m.data.Tenant.Projects) {
		return ""
	}
	return m.data.Tenant.Projects[m.cursor].Name
}

func (m tuiModel) selectedRouteHostname() string {
	if len(m.data.Routes) == 0 || m.cursor >= len(m.data.Routes) {
		return ""
	}
	return m.data.Routes[m.cursor].Hostname
}

func (m tuiModel) machineActions() []tuiAction {
	ref := m.selectedMachineRef()
	actions := []tuiAction{
		{Label: "Create Machine", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Create Machine", "Machine name or project:machine", "", func(value string) tea.Cmd {
				return model.runCommand([]string{"create", value, "--detach"})
			})
		}},
	}
	if ref != "" {
		actions = append(actions,
			tuiAction{Label: "Connect", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.runExternalCommand([]string{"connect", ref})
			}},
			tuiAction{Label: "Status", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"status", ref})
			}},
			tuiAction{Label: "Start", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"start", ref})
			}},
			tuiAction{Label: "Stop", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"stop", ref})
			}},
			tuiAction{Label: "Restart", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"restart", ref})
			}},
			tuiAction{Label: "Set App Port", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.prompt("Set App Port", "Enter port number for "+ref, "", func(value string) tea.Cmd {
					return model.runCommand([]string{"port", "set", ref, value})
				})
			}},
			tuiAction{Label: "Enable Workload Identity", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.prompt("Enable Workload Identity", "Cloud Identity Config name, for example gcp", "gcp", func(value string) tea.Cmd {
					args := []string{"workload", "enable", ref}
					if value != "" {
						args = append(args, "--cloud-identity", value)
					}
					return model.runExternalCommand(args)
				})
			}},
			tuiAction{Label: "Create Host Override", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.prompt("Create Host Override", "Hostname to map to "+ref, "", func(value string) tea.Cmd {
					return model.runCommand([]string{"host", "override", "create", ref, value})
				})
			}},
			tuiAction{Label: "Delete Host Override", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.prompt("Delete Host Override", "Hostname to remove for "+ref, "", func(value string) tea.Cmd {
					return model.runCommand([]string{"host", "override", "delete", ref, value})
				})
			}},
			tuiAction{Label: "Delete", Help: ref, Run: func(model *tuiModel) tea.Cmd {
				return model.confirm("Delete Machine "+ref+"?", "This removes the Machine.", model.runCommand([]string{"delete", ref, "--yes"}))
			}},
		)
	}
	return actions
}

func (m tuiModel) projectActions() []tuiAction {
	projectName := m.selectedProjectName()
	actions := []tuiAction{
		{Label: "Create Project", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Create Project", "Project name", "", func(value string) tea.Cmd {
				return model.runCommand([]string{"project", "create", value})
			})
		}},
	}
	if projectName != "" {
		actions = append(actions,
			tuiAction{Label: "Switch Current Project", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.switchProject(projectName)
			}},
			tuiAction{Label: "Project Status", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"project", "status", projectName})
			}},
			tuiAction{Label: "Set Cloud Identity", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.prompt("Set Cloud Identity", "Config name, for example gcp", "", func(value string) tea.Cmd {
					return model.runCommand([]string{"project", "set-cloud-identity", projectName, value})
				})
			}},
			tuiAction{Label: "Unset Cloud Identity", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"project", "unset-cloud-identity", projectName})
			}},
			tuiAction{Label: "Docker Autostart On", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"project", "set-docker-autostart", projectName, "on"})
			}},
			tuiAction{Label: "Docker Autostart Off", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"project", "set-docker-autostart", projectName, "off"})
			}},
			tuiAction{Label: "Delete Project", Help: projectName, Run: func(model *tuiModel) tea.Cmd {
				return model.confirm("Delete Project "+projectName+"?", "The project must be empty.", model.runCommand([]string{"project", "delete", projectName, "--yes"}))
			}},
		)
	}
	return actions
}

func (m tuiModel) routeActions() []tuiAction {
	hostname := m.selectedRouteHostname()
	actions := []tuiAction{
		{Label: "Create Public Route", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Create Public Route", "hostname project:machine", "", func(value string) tea.Cmd {
				parts := strings.Fields(value)
				if len(parts) != 2 {
					return model.commandMsg([]string{"route", "create"}, "", fmt.Errorf("expected: hostname project:machine"))
				}
				return model.runCommand([]string{"route", "create", parts[0], parts[1]})
			})
		}},
	}
	if hostname != "" {
		actions = append(actions,
			tuiAction{Label: "Route Status", Help: hostname, Run: func(model *tuiModel) tea.Cmd {
				return model.runCommand([]string{"route", "status", hostname})
			}},
			tuiAction{Label: "Delete Route", Help: hostname, Run: func(model *tuiModel) tea.Cmd {
				return model.confirm("Delete Public Route "+hostname+"?", "", model.runCommand([]string{"route", "delete", hostname}))
			}},
		)
	}
	return actions
}

func (m tuiModel) shareActions() []tuiAction {
	return []tuiAction{
		{Label: "List Shares", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"share", "list"})
		}},
		{Label: "List Outbound Shares", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"share", "list", "--outbound"})
		}},
		{Label: "List Inbound Shares", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"share", "list", "--inbound"})
		}},
		{Label: "List Share Offers", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"share", "offers"})
		}},
		{Label: "Create Share", Help: "source --to tenant [--name name]", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Create Share", "Example: default:/workspace/app --to acme --name app", "", func(value string) tea.Cmd {
				args := append([]string{"share", "create"}, strings.Fields(value)...)
				return model.runCommand(args)
			})
		}},
		{Label: "Share Status", Help: "project/share-name or source-tenant/source-project/share-name", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Share Status", "Share reference", "", func(value string) tea.Cmd {
				return model.runCommand([]string{"share", "status", value})
			})
		}},
		{Label: "Accept Share Offer", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Accept Share Offer", "source-tenant/source-project/share-name", "", func(value string) tea.Cmd {
				return model.runCommand([]string{"share", "accept", value})
			})
		}},
		{Label: "Decline Share Offer", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Decline Share Offer", "source-tenant/source-project/share-name", "", func(value string) tea.Cmd {
				return model.runCommand([]string{"share", "decline", value})
			})
		}},
		{Label: "Reconcile Shares", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"share", "reconcile"})
		}},
		{Label: "Delete Share", Help: "project/share-name", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Delete Share", "project/share-name", "", func(value string) tea.Cmd {
				return model.runCommand([]string{"share", "delete", value, "--yes"})
			})
		}},
	}
}

func (m tuiModel) setupActions() []tuiAction {
	return []tuiAction{
		{Label: "Login", Help: "browser-assisted CLI Device Login", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Login", "Auth Hostname or URL", commandAuthHostname(model.config, ""), func(value string) tea.Cmd {
				return model.runExternalCommand([]string{"login", value})
			})
		}},
		{Label: "List Accessible Tenants", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"tenant", "list"})
		}},
		{Label: "DNS Install/Refresh", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"dns", "refresh"})
		}},
		{Label: "Trust Install", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"trust", "install"})
		}},
		{Label: "Trust Uninstall", Run: func(model *tuiModel) tea.Cmd {
			return model.confirm("Uninstall Tenant CA trust?", "", model.runCommand([]string{"trust", "uninstall"}))
		}},
		{Label: "Tailscale Status", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"tailscale", "status"})
		}},
		{Label: "Tailscale Up", Run: func(model *tuiModel) tea.Cmd {
			return model.runExternalCommand([]string{"tailscale", "up"})
		}},
		{Label: "Tailscale Down", Run: func(model *tuiModel) tea.Cmd {
			return model.confirm("Run tailscale down?", "This detaches the local host from the Tenant Tailnet.", model.runCommand([]string{"tailscale", "down"}))
		}},
		{Label: "Clear Cache", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"cache", "clear"})
		}},
		{Label: "Set SSH Public Key", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Set SSH Public Key", "Path to public key, empty uses default key discovery", "", func(value string) tea.Cmd {
				if value == "" {
					return model.runCommand([]string{"ssh-key", "set"})
				}
				return model.runCommand([]string{"ssh-key", "set", "--file", value})
			})
		}},
		{Label: "List Host Overrides", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"host", "override", "list"})
		}},
		{Label: "GCP Cloud Identity Setup", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("GCP Cloud Identity Setup", "Extra args, for example: --gcp-project my-project --service-account name", "", func(value string) tea.Cmd {
				args := append([]string{"cloud-identity", "gcp", "setup"}, strings.Fields(value)...)
				return model.runExternalCommand(args)
			})
		}},
	}
}

func (m tuiModel) configActions() []tuiAction {
	return []tuiAction{
		{Label: "Show Config", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"config", "show"})
		}},
		{Label: "Set Tenant", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Set Tenant", "Tenant name", model.config.adminConfig.Tenant, func(value string) tea.Cmd {
				return model.runCommand([]string{"config", "set", "tenant", value})
			})
		}},
		{Label: "Set Project", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Set Project", "Project name", currentProjectName(model.config.adminConfig.Project), func(value string) tea.Cmd {
				return model.runCommand([]string{"config", "set", "project", value})
			})
		}},
		{Label: "Set Remote", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Set Remote", "Sandcastle user remote", model.config.adminConfig.Remote, func(value string) tea.Cmd {
				return model.runCommand([]string{"config", "set", "remote", value})
			})
		}},
		{Label: "Set Auth Hostname", Run: func(model *tuiModel) tea.Cmd {
			return model.prompt("Set Auth Hostname", "Auth App hostname or URL", commandAuthHostname(model.config, ""), func(value string) tea.Cmd {
				return model.runCommand([]string{"config", "set", "auth.hostname", value})
			})
		}},
		{Label: "Unset Project", Run: func(model *tuiModel) tea.Cmd {
			return model.runCommand([]string{"config", "unset", "project"})
		}},
	}
}

func (m tuiModel) switchTenant(name string) tea.Cmd {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	args := []string{"tenant", "switch", name}
	if strings.TrimSpace(m.config.adminConfig.AuthToken) == "" {
		args = append(args, "--local-only")
	}
	return m.runCommand(args)
}

func (m tuiModel) switchProject(name string) tea.Cmd {
	return func() tea.Msg {
		cfgPath := scconfig.DefaultConfigPath()
		cfg, err := scconfig.LoadSandcastleConfig(cfgPath)
		if err == nil {
			cfg.Project = name
			err = scconfig.SaveSandcastleConfig(cfgPath, cfg)
		}
		output := fmt.Sprintf("Current Project set to %q in %s", name, cfgPath)
		return tuiCommandMsg{args: []string{"config", "set", "project", name}, output: output, err: err}
	}
}

func (m tuiModel) runCommand(args []string) tea.Cmd {
	return func() tea.Msg {
		output, err := executeTUICommand(m.ctx, m.config, args)
		return tuiCommandMsg{args: args, output: output, err: err}
	}
}

func (m tuiModel) commandMsg(args []string, output string, err error) tea.Cmd {
	return func() tea.Msg {
		return tuiCommandMsg{args: args, output: output, err: err}
	}
}

func (m tuiModel) runExternalCommand(args []string) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return m.commandMsg(args, "", err)
	}
	cmd := exec.CommandContext(m.ctx, exe, args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return tuiExternalCommandMsg{args: args, err: err}
	})
}

func executeTUICommand(ctx context.Context, config commandConfig, args []string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config.stdout = &stdout
	config.stderr = &stderr
	config.stdin = strings.NewReader("")
	config.stdinIsTerminal = func(io.Reader) bool { return false }
	config.stdoutIsTerminal = func(io.Writer) bool { return false }
	config.interactiveHome = nil
	cmd := NewRootCommand(config)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	output := strings.TrimSpace(stdout.String())
	if text := strings.TrimSpace(stderr.String()); text != "" {
		if output != "" {
			output += "\n"
		}
		output += text
	}
	return output, err
}

func selectedTenantIndex(tenants []tenant.Summary, current string) int {
	for i, tenant := range tenants {
		if tenant.Tenant == current {
			return i
		}
	}
	return 0
}

func selectedProjectIndex(projects []meta.Project, current string) int {
	current = currentProjectName(current)
	for i, project := range projects {
		if project.Name == current {
			return i
		}
	}
	return 0
}

func currentProjectName(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return "default"
	}
	return project
}
