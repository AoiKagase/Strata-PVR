//go:build !windows

package system

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func DropPrivileges(uid, gid any) error {
	if os.Getuid() != 0 {
		return nil
	}
	groupID, err := resolveGroupID(gid)
	if err != nil {
		return err
	}
	if err := syscall.Setgid(groupID); err != nil {
		return err
	}
	userID, ok, err := resolveUserID(uid)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("[fatal] 'uid' required in config.")
	}
	return syscall.Setuid(userID)
}

func resolveGroupID(value any) (int, error) {
	if value == nil {
		value = "video"
	}
	if id, _, err := numericID(value); err == nil {
		return id, nil
	}
	name, ok := value.(string)
	if !ok {
		return 0, fmt.Errorf("unsupported gid type %T", value)
	}
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func resolveUserID(value any) (int, bool, error) {
	if id, ok, err := numericID(value); err == nil {
		return id, ok, nil
	}
	name, ok := value.(string)
	if !ok {
		return 0, true, fmt.Errorf("unsupported uid type %T", value)
	}
	account, err := user.Lookup(name)
	if err != nil {
		return 0, true, err
	}
	id, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, true, err
	}
	return id, true, nil
}
