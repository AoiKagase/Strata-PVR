package system

import (
	"fmt"
	"strconv"
)

func numericID(value any) (int, bool, error) {
	switch v := value.(type) {
	case nil:
		return 0, false, nil
	case int:
		return v, true, nil
	case int64:
		return int(v), true, nil
	case float64:
		if v != float64(int(v)) {
			return 0, true, fmt.Errorf("id must be an integer: %v", v)
		}
		return int(v), true, nil
	case string:
		id, err := strconv.Atoi(v)
		if err == nil {
			return id, true, nil
		}
		return 0, true, err
	default:
		return 0, true, fmt.Errorf("unsupported id type %T", value)
	}
}
