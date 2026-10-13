//go:build windows

package nativeui

import "github.com/lxn/walk"

type rowsModel struct {
	walk.TableModelBase
	rows    [][]any
	checks  []bool
	onCheck func(int, bool)
}

func (m *rowsModel) RowCount() int { return len(m.rows) }
func (m *rowsModel) Value(r, c int) any {
	if r < 0 || r >= len(m.rows) || c < 0 || c >= len(m.rows[r]) {
		return ""
	}
	return m.rows[r][c]
}
func (m *rowsModel) Checked(i int) bool { return i >= 0 && i < len(m.checks) && m.checks[i] }
func (m *rowsModel) SetChecked(i int, v bool) error {
	if i >= 0 && i < len(m.checks) {
		m.checks[i] = v
		if m.onCheck != nil {
			m.onCheck(i, v)
		}
	}
	return nil
}
func (m *rowsModel) reset(rows [][]any, checked []bool) {
	m.rows = rows
	m.checks = checked
	m.PublishRowsReset()
}
