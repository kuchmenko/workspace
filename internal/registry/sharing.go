package registry

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

func validateShareableHistory(tx *sql.Tx, workspaceID string) error {
	rows, err := tx.Query(`SELECT snapshot FROM revisions WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var snapshot []byte
		if err = rows.Scan(&snapshot); err != nil {
			return err
		}
		if err = validateShareableSnapshot(snapshot); err != nil {
			return err
		}
	}
	return rows.Err()
}

func policySharedWithOtherDevice(policy AccessPolicy, localID string) bool {
	if policy.Mode == AccessAll {
		return true
	}
	for deviceID := range policy.Roles {
		if deviceID != localID {
			return true
		}
	}
	return false
}

func validateShareableSnapshot(body []byte) error {
	state, err := decodeSnapshot(body)
	if err != nil {
		return err
	}
	for name, project := range state.Projects {
		if remoteContainsCredentials(project.Remote) {
			return fmt.Errorf("project %q remote contains credentials", name)
		}
		for mirror, remote := range project.Mirrors {
			if remoteContainsCredentials(remote) {
				return fmt.Errorf("project %q mirror %q contains credentials", name, mirror)
			}
		}
	}
	return nil
}

func remoteContainsCredentials(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return false
	}
	_, password := parsed.User.Password()
	return password || strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}
