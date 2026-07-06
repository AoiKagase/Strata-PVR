//go:build windows

package system

func DropPrivileges(_, _ any) error {
	return nil
}
