package gui

import "embed"

// Assets 是前端静态文件（gui/frontend/dist 下的 index.html/app.js/styles.css）。
// 构建前确保 dist 已就绪（本仓库前端为免构建的纯静态文件，直接提交）。
//
//go:embed all:frontend/dist
var Assets embed.FS
