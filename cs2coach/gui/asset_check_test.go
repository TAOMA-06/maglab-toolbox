package gui

import "testing"

func TestAssetsEmbedded(t *testing.T) {
	for _, f := range []string{"frontend/dist/index.html", "frontend/dist/app.js", "frontend/dist/styles.css"} {
		if _, err := Assets.ReadFile(f); err != nil {
			t.Errorf("前端文件缺失 %s: %v", f, err)
		}
	}
}
