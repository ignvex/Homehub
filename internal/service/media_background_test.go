package service

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// mediaRows 构造 ListByIDs 需要的行。
func mediaRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "filename", "original_name", "file_path", "file_size", "mime_type", "media_type"})
}

// TestListBackgroundAssetsFiltersKinds 确认输出层过滤掉音频与 PDF。
func TestListBackgroundAssetsFiltersKinds(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewMediaService(db)

	mock.ExpectQuery(`SELECT \* FROM "media_assets" WHERE id IN`).
		WillReturnRows(mediaRows().
			AddRow(1, "a.png", "a.png", "uploads/a.png", 100, "image/png", "image").
			AddRow(2, "b.mp3", "b.mp3", "uploads/b.mp3", 200, "audio/mpeg", "audio").
			AddRow(3, "c.mp4", "c.mp4", "uploads/c.mp4", 300, "video/mp4", "video").
			AddRow(4, "d.pdf", "d.pdf", "uploads/d.pdf", 400, "application/pdf", "document"))

	assets, err := svc.ListBackgroundAssets([]uint64{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("ListBackgroundAssets: %v", err)
	}

	if len(assets) != 2 {
		t.Fatalf("应只返回图片与视频，实际 %d 条: %+v", len(assets), assets)
	}
	if assets[0].ID != 1 || assets[1].ID != 3 {
		t.Errorf("返回的资源顺序或内容不符: %+v", assets)
	}
}

// TestFilterBackgroundIDsDropsInvalid 确认保存前清洗掉非视觉素材与不存在的 ID。
func TestFilterBackgroundIDsDropsInvalid(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewMediaService(db)

	mock.ExpectQuery(`SELECT \* FROM "media_assets" WHERE id IN`).
		WillReturnRows(mediaRows().
			AddRow(1, "a.png", "a.png", "uploads/a.png", 100, "image/png", "image").
			AddRow(2, "b.mp3", "b.mp3", "uploads/b.mp3", 200, "audio/mpeg", "audio"))

	// 1 存在且是图片；2 是音频；99 不存在
	got, err := svc.FilterBackgroundIDs([]uint64{1, 2, 99})
	if err != nil {
		t.Fatalf("FilterBackgroundIDs: %v", err)
	}

	if len(got) != 1 || got[0] != 1 {
		t.Errorf("应只保留图片 ID 1，实际 %v", got)
	}
}

// TestFilterBackgroundIDsEmpty 确认空输入不查库。
func TestFilterBackgroundIDsEmpty(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewMediaService(db)

	got, err := svc.FilterBackgroundIDs(nil)
	if err != nil {
		t.Fatalf("FilterBackgroundIDs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("空输入应返回空结果，实际 %v", got)
	}
}
