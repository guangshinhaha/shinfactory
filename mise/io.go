package mise

import (
	"github.com/guangshinhaha/shinfactory/internal/jsonx"
)

func writeBriefAtomic(path string, brief Brief) error {
	return jsonx.WriteJSONIndented(path, brief)
}
