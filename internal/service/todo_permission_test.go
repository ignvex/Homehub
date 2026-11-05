package service

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 测试用操作者。ID 互不相同，便于在 SQL 参数里认出是谁。
var (
	editor = &model.User{BaseModel: model.BaseModel{ID: 3}, Level: model.LevelEditor}
	member = &model.User{BaseModel: model.BaseModel{ID: 4}, Level: model.LevelMember}
	viewer = &model.User{BaseModel: model.BaseModel{ID: 5}, Level: model.LevelViewer}
)

var todoCols = []string{"id", "title", "status", "created_by", "assignee_id"}

// 4 级改待办时，归属条件必须写进 UPDATE 本身（created_by = me），
// 而不是先查后改——两步之间归属可能变化。sqlmock 证明 SQL 真的带了它。
func TestMemberUpdateIsScopedToOwnTodosInSQL(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" SET "title"=\$1,"updated_at"=\$2 WHERE id = \$3 AND created_by = \$4`).
		WithArgs("t", sqlmock.AnyArg(), 7, 4).
		WillReturnResult(sqlmock.NewResult(0, 1))

	title := "t"
	if err := NewTodoService(db).UpdateTodo(member, 7, TodoUpdate{Title: &title}); err != nil {
		t.Fatalf("改自己的待办应成功: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 4 级改别人的待办：UPDATE 没命中 → 再查一次确认存在 → 403，而不是 404。
// 报 404 会让人以为待办被删了。
func TestMemberCannotEditOthersTodo(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" .* AND created_by = \$\d`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT \* FROM "todos"`).
		WillReturnRows(sqlmock.NewRows(todoCols).AddRow(7, "x", "pending", 99, nil))

	title := "hijack"
	err := NewTodoService(db).UpdateTodo(member, 7, TodoUpdate{Title: &title})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，got %v", err)
	}
}

// 只改状态时，4 级可以改「指派给自己」的待办：条件放宽为
// created_by = me OR assignee_id = me。
func TestMemberCanChangeStatusOfAssignedTodo(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" SET "status"=\$1,"updated_at"=\$2 WHERE id = \$3 AND \(\(created_by = \$4 OR assignee_id = \$5\)\) AND "todos"."deleted_at" IS NULL$`).
		WithArgs("completed", sqlmock.AnyArg(), 7, 4, 4).
		WillReturnResult(sqlmock.NewResult(0, 1))

	status := "completed"
	if err := NewTodoService(db).UpdateTodo(member, 7, TodoUpdate{Status: &status}); err != nil {
		t.Fatalf("改指派给自己的待办状态应成功: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 状态之外再带一个字段，就退回「只能改自己创建的」：被指派者不能顺手
// 改标题或把待办转指派给别人。
func TestMemberAssigneeCannotEditOtherFields(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`UPDATE "todos" SET "assignee_id"=\$1,"status"=\$2,"updated_at"=\$3 WHERE id = \$4 AND created_by = \$5 AND "todos"."deleted_at" IS NULL$`).
		WithArgs(8, "completed", sqlmock.AnyArg(), 7, 4).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT \* FROM "todos"`).
		WillReturnRows(sqlmock.NewRows(todoCols).AddRow(7, "x", "pending", 99, 4))

	status := "completed"
	other := uint64(8)
	err := NewTodoService(db).UpdateTodo(member, 7, TodoUpdate{Status: &status, Assignee: Optional[uint64]{Set: true, Value: &other}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("被指派者改状态以外的字段应被拒，got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 3 级不受归属限制：SQL 里不得出现 created_by 条件。
func TestEditorUpdateHasNoOwnershipFilter(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" SET "title"=\$1,"updated_at"=\$2 WHERE id = \$3 AND "todos"."deleted_at" IS NULL$`).
		WithArgs("t", sqlmock.AnyArg(), 7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	title := "t"
	if err := NewTodoService(db).UpdateTodo(editor, 7, TodoUpdate{Title: &title}); err != nil {
		t.Fatalf("3 级应能改任意待办: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 4 级删除：同样把 created_by = me 写进 DELETE（软删除 UPDATE）。
// created_by 为 NULL 的旧数据永远不满足这个条件，只有 3 级能删。
func TestMemberDeleteIsScopedToOwnTodos(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "todos" SET "deleted_at"=\$1 WHERE id = \$2 AND created_by = \$3`).
		WithArgs(sqlmock.AnyArg(), 7, 4).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT \* FROM "todos"`).
		WillReturnRows(sqlmock.NewRows(todoCols).AddRow(7, "legacy", "pending", nil, nil))

	if err := NewTodoService(db).DeleteTodo(member, 7); !errors.Is(err, ErrForbidden) {
		t.Fatalf("4 级删除无归属的旧待办应被拒，got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 4 级重排：只要有一个 id 不是自己创建的，整体拒绝，且不发 UPDATE。
func TestMemberReorderRejectedUnlessAllOwned(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "todos" WHERE \(id IN \(\$1,\$2,\$3\) AND created_by = \$4\)`).
		WithArgs(1, 2, 3, 4).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	if err := NewTodoService(db).ReorderTodos(member, []uint64{1, 2, 3}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("含他人待办的重排应被拒，got %v", err)
	}
	// 没有为 UPDATE 设期望：若被执行，sqlmock 会报未预期调用。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

func TestMemberReorderAllowedWhenAllOwned(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "todos"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec(`UPDATE todos SET sort_order = CASE id`).
		WillReturnResult(sqlmock.NewResult(0, 2))

	// 重复 id 去重后再比较数量，否则 [1,1] 会被误判为越权。
	if err := NewTodoService(db).ReorderTodos(member, []uint64{2, 1, 1}); err != nil {
		t.Fatalf("全部是自己的待办时重排应成功: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 5 级、非法等级（含 0）与 nil 操作者一律拒绝，且不碰数据库。
// Level=0 是最容易出事的：0 <= 3 会被当成最高权限。
func TestTodoWritesFailClosedForViewerAndInvalidLevels(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewTodoService(db)

	title := "x"
	for _, op := range []*model.User{
		nil,
		viewer,
		{BaseModel: model.BaseModel{ID: 9}, Level: 0},
		{BaseModel: model.BaseModel{ID: 9}, Level: 6},
	} {
		if _, err := svc.CreateTodo(op, CreateTodoInput{Title: "x"}); !errors.Is(err, ErrForbidden) {
			t.Errorf("CreateTodo(%+v) = %v", op, err)
		}
		if err := svc.UpdateTodo(op, 1, TodoUpdate{Title: &title}); !errors.Is(err, ErrForbidden) {
			t.Errorf("UpdateTodo(%+v) = %v", op, err)
		}
		if err := svc.DeleteTodo(op, 1); !errors.Is(err, ErrForbidden) {
			t.Errorf("DeleteTodo(%+v) = %v", op, err)
		}
		if err := svc.ReorderTodos(op, []uint64{1}); !errors.Is(err, ErrForbidden) {
			t.Errorf("ReorderTodos(%+v) = %v", op, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝路径不应发 SQL: %v", err)
	}
}

// 创建时 created_by 记为操作者，客户端无法指定。
func TestCreateTodoRecordsCreator(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`MAX\(sort_order\)`).WillReturnRows(sqlmock.NewRows([]string{"m"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "todos" .*"created_by","assignee_id"`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), nil, "t", "", "pending", 0, nil, 1, 4, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
	mock.ExpectQuery(`SELECT "id","username" FROM "users" WHERE id IN \(\$1\)`).
		WithArgs(4).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(4, "mia"))

	todo, err := NewTodoService(db).CreateTodo(member, CreateTodoInput{Title: "t"})
	if err != nil {
		t.Fatalf("CreateTodo 失败: %v", err)
	}
	if todo.CreatedBy == nil || *todo.CreatedBy != 4 || todo.Creator == nil || todo.Creator.Username != "mia" {
		t.Fatalf("created_by / creator 不符: %+v", todo)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 列表补用户信息是一条 IN 查询，与条数无关（不是 N+1）。
// 过滤条件 assignee / mine 进 WHERE。
func TestListTodosAttachesUsersInOneQuery(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	me := uint64(4)
	mock.ExpectQuery(`SELECT \* FROM "todos" WHERE assignee_id = \$1 AND created_by = \$2 AND "todos"."deleted_at" IS NULL ORDER BY priority DESC, sort_order ASC`).
		WithArgs(4, 4).
		WillReturnRows(sqlmock.NewRows(todoCols).
			AddRow(1, "a", "pending", 4, 4).
			AddRow(2, "b", "pending", 4, 6).
			AddRow(3, "c", "pending", nil, nil))
	mock.ExpectQuery(`SELECT "id","username" FROM "users" WHERE id IN \(\$1,\$2\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(4, "mia").AddRow(6, "zed"))

	todos, err := NewTodoService(db).ListTodos(TodoFilter{AssigneeID: &me, CreatedBy: &me})
	if err != nil {
		t.Fatalf("ListTodos 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足（多出的用户查询说明退化成了 N+1）: %v", err)
	}
	if todos[1].Assignee == nil || todos[1].Assignee.Username != "zed" || todos[2].Creator != nil {
		t.Fatalf("用户信息补齐不符: %+v", todos)
	}
}

// 用户目录只含启用用户，只取 id 与 username——邮箱和等级不随指派选择器泄露。
func TestDirectoryListsActiveUsersOnly(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT "id","username" FROM "users" WHERE is_active = \$1 AND "users"."deleted_at" IS NULL ORDER BY username ASC`).
		WithArgs(true).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(1, "admin").AddRow(4, "mia"))

	users, err := NewAuthService(db).Directory()
	if err != nil {
		t.Fatalf("Directory 失败: %v", err)
	}
	if len(users) != 2 || users[1].Username != "mia" {
		t.Fatalf("目录不符: %+v", users)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 真实数据库：4 级的归属边界与删除用户后的归属置空。
func TestIntegrationTodoOwnershipAndUserDeletion(t *testing.T) {
	db := stateIntegrationDB(t)

	admin := model.User{Username: "admin", PasswordHash: "x", Level: model.LevelAdmin, IsActive: true}
	mia := model.User{Username: "mia", PasswordHash: "x", Level: model.LevelMember, IsActive: true}
	zed := model.User{Username: "zed", PasswordHash: "x", Level: model.LevelMember, IsActive: true}
	for _, u := range []*model.User{&admin, &mia, &zed} {
		must(t, db.Create(u).Error)
	}
	svc := NewTodoService(db)

	zedID := zed.ID
	mine, err := svc.CreateTodo(&mia, CreateTodoInput{Title: "mia's"})
	must(t, err)
	assigned, err := svc.CreateTodo(&admin, CreateTodoInput{Title: "for zed", AssigneeID: &zedID})
	must(t, err)
	legacy := model.Todo{Title: "legacy", Status: "pending"}
	must(t, db.Create(&legacy).Error)

	title := "renamed"
	status := "completed"
	// mia 改自己的：成功；改 admin 的：403；动旧数据：403。
	must(t, svc.UpdateTodo(&mia, mine.ID, TodoUpdate{Title: &title}))
	if err := svc.UpdateTodo(&mia, assigned.ID, TodoUpdate{Title: &title}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mia 改别人的待办应 403，got %v", err)
	}
	if err := svc.DeleteTodo(&mia, legacy.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mia 删旧数据应 403，got %v", err)
	}
	if err := svc.UpdateTodo(&mia, 999999, TodoUpdate{Title: &title}); !errors.Is(err, ErrTodoNotFound) {
		t.Fatalf("不存在的待办应 404，got %v", err)
	}
	// zed 是被指派者：可改状态，不能改标题。
	must(t, svc.UpdateTodo(&zed, assigned.ID, TodoUpdate{Status: &status}))
	if err := svc.UpdateTodo(&zed, assigned.ID, TodoUpdate{Title: &title}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("被指派者改标题应 403，got %v", err)
	}
	// 重排：含他人的整体拒绝。
	if err := svc.ReorderTodos(&mia, []uint64{mine.ID, legacy.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("含旧数据的重排应 403，got %v", err)
	}
	must(t, svc.ReorderTodos(&mia, []uint64{mine.ID}))

	// 过滤
	list, err := svc.ListTodos(TodoFilter{AssigneeID: &zedID})
	must(t, err)
	if len(list) != 1 || list[0].ID != assigned.ID || list[0].Assignee == nil || list[0].Assignee.Username != "zed" || list[0].Creator.Username != "admin" {
		t.Fatalf("assignee 过滤或用户补齐不符: %+v", list)
	}

	// 删除 zed：指派置空；删除 mia：创建者置空，此后 mia 的旧待办只有 3 级能动。
	auth := NewAuthService(db)
	must(t, auth.DeleteUser(zed.ID, &admin))
	must(t, auth.DeleteUser(mia.ID, &admin))
	var got model.Todo
	must(t, db.First(&got, assigned.ID).Error)
	if got.AssigneeID != nil {
		t.Fatalf("删除用户后 assignee_id 应为 NULL: %v", *got.AssigneeID)
	}
	var gotMine model.Todo
	must(t, db.First(&gotMine, mine.ID).Error)
	if gotMine.CreatedBy != nil {
		t.Fatalf("删除用户后 created_by 应为 NULL: %v", *gotMine.CreatedBy)
	}

	dir, err := auth.Directory()
	must(t, err)
	if len(dir) != 1 || dir[0].Username != "admin" {
		t.Fatalf("目录应只剩启用且未删除的 admin: %+v", dir)
	}
}
