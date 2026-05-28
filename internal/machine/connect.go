package machine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type ConnectRequest struct {
	Reference string
	Command   []string
}

type ConnectPlan struct {
	Reference          string         `json:"reference"`
	Tenant             tenant.Summary `json:"tenant"`
	Project            string         `json:"project"`
	Name               string         `json:"name"`
	InstanceName       string         `json:"instanceName"`
	Hostname           string         `json:"hostname,omitempty"`
	PrivateIP          string         `json:"privateIP,omitempty"`
	SSHHost            string         `json:"sshHost,omitempty"`
	HostKeyAlias       string         `json:"hostKeyAlias,omitempty"`
	CloudIdentity      string         `json:"cloudIdentity,omitempty"`
	Command            []string       `json:"command"`
	LinuxUser          string         `json:"linuxUser"`
	UserID             int            `json:"userId"`
	GroupID            int            `json:"groupId"`
	WorkingDir         string         `json:"workingDir"`
	Interactive        bool           `json:"interactive"`
	Managed            bool           `json:"managed"`
	StartBeforeConnect bool           `json:"startBeforeConnect,omitempty"`
}

type ConnectSession struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Connector interface {
	ConnectMachine(context.Context, ConnectPlan, ConnectSession) error
}

func PlanConnect(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, machineStore Store, request ConnectRequest) (ConnectPlan, error) {
	if err := admin.Validate(); err != nil {
		return ConnectPlan{}, err
	}
	resolved, err := resolveExistingMachine(ctx, admin, store, machineStore, request.Reference)
	if err != nil {
		return ConnectPlan{}, err
	}
	command := request.Command
	interactive := false
	if len(command) == 0 {
		command = []string{"/bin/bash", "-l"}
		interactive = true
	}
	if len(command) == 0 || command[0] == "" {
		return ConnectPlan{}, fmt.Errorf("connect command is required")
	}
	linuxUser := managedLinuxUser(resolved)
	workingDir := "/workspace"
	userID := DefaultLinuxUID
	groupID := DefaultLinuxGID
	if !resolved.Managed {
		linuxUser = "root"
		workingDir = "/root"
		userID = 0
		groupID = 0
	}
	hostname := ""
	sshHost := resolved.PrivateIP
	hostKeyAlias := ""
	if resolved.Managed {
		hostname = resolved.Name + "." + resolved.Project + "." + resolved.Summary.DNSSuffix
		hostKeyAlias = hostname
	}
	return ConnectPlan{
		Reference:          request.Reference,
		Tenant:             resolved.Summary,
		Project:            resolved.Project,
		Name:               resolved.Name,
		InstanceName:       resolved.InstanceName,
		Hostname:           hostname,
		PrivateIP:          resolved.PrivateIP,
		SSHHost:            sshHost,
		HostKeyAlias:       hostKeyAlias,
		CloudIdentity:      resolved.CloudIdentity,
		Command:            command,
		LinuxUser:          linuxUser,
		UserID:             userID,
		GroupID:            groupID,
		WorkingDir:         workingDir,
		Interactive:        interactive,
		Managed:            resolved.Managed,
		StartBeforeConnect: resolved.Managed && !resolved.Running,
	}, nil
}

func managedLinuxUser(resolved resolvedMachine) string {
	if user := strings.TrimSpace(resolved.LinuxUser); user != "" {
		return user
	}
	if user := strings.TrimSpace(resolved.Summary.UnixUser); user != "" {
		return user
	}
	return resolved.Summary.Tenant
}
