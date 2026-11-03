package main

import (
	"fmt"

	"github.com/zhangyf/jrp/internal/offline"
)

// 离线 Excel 闭环：导出时把 plan_date / language / mode 藏进 _meta sheet，
// 回填时读回来定位这次是哪天的练习。
//
// 为什么用隐藏 sheet 而不是文件名或文档属性：
//   - 文件名：微信/邮件转发时经常被改名，plan_date 就没了
//   - 文档属性：WPS 会剥离自定义属性，往返一次就丢
//   - 隐藏 sheet：跟着文件走，excelize 稳定可读，且对现有两个 sheet 的布局零干扰

// writeMetaSheet 写入并隐藏 _meta sheet。
func (w *excelWriter) writeMetaSheet(meta *offline.ExcelMeta) error {
	if _, err := w.f.NewSheet(offline.MetaSheetName); err != nil {
		return err
	}
	rows := [][2]string{
		{"plan_date", meta.PlanDate},
		{"language", meta.Language},
		{"mode", meta.Mode},
		{"kind", meta.Kind},
	}
	for i, kv := range rows {
		cell := fmt.Sprintf("A%d", i+1)
		if err := w.f.SetCellValue(offline.MetaSheetName, cell, kv[0]); err != nil {
			return err
		}
		if err := w.f.SetCellValue(offline.MetaSheetName, fmt.Sprintf("B%d", i+1), kv[1]); err != nil {
			return err
		}
	}
	return w.f.SetSheetVisible(offline.MetaSheetName, false)
}
