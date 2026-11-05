package service

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 清空描述与截止日期曾经做不到：空串被当成「不改」，due_date 只有
// *time.Time，缺失与 null 都绑成 nil。这里证明清空时 SQL 真的写了
// description = ” 与 due_date = NULL，而不是跳过这两列。
func TestUpdateTodoClearsDescriptionAndDueDate(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" SET "description"=\$1,"due_date"=\$2,"updated_at"=\$3 WHERE id = \$4`).
		WithArgs("", nil, sqlmock.AnyArg(), 7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := NewTodoService(db).UpdateTodo(7, TodoUpdate{
		Description: Optional[string]{Set: true},
		DueDate:     Optional[time.Time]{Set: true},
	})
	if err != nil {
		t.Fatalf("UpdateTodo 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 缺失的字段不得出现在 SET 里：否则「只改标题」会顺手把描述清掉。
func TestUpdateTodoLeavesMissingFieldsUntouched(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" SET "title"=\$1,"updated_at"=\$2 WHERE id = \$3`).
		WithArgs("新标题", sqlmock.AnyArg(), 7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	title := "新标题"
	if err := NewTodoService(db).UpdateTodo(7, TodoUpdate{Title: &title}); err != nil {
		t.Fatalf("UpdateTodo 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}
