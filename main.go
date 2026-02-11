// main.go
package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---------- Models ----------

type Article struct {
	SQLID      uint      `gorm:"primaryKey;column:sqlid" json:"id"`
	Title      string    `json:"title" gorm:"column:title;index"`
	Content    string    `json:"content" gorm:"column:content;type:text"`
	AuthorID   uint      `json:"author_id" gorm:"column:author_id;index"`
	CategoryID uint      `json:"category_id" gorm:"column:category_id;index"`
	Status     string    `json:"status" gorm:"column:status;default:draft;index"` // draft|published|archived
	ViewCount  int64     `json:"view_count" gorm:"column:view_count;default:0"`
	CreatedAt  time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}

func (Article) TableName() string { return "articles" }

type Comment struct {
	ID              uint       `gorm:"primaryKey;autoIncrement" json:"id"` // 新增主键
	SQLArticleID    uint       `json:"article_id" gorm:"column:sqlarticle_id;index;not null"`
	UserID          uint       `json:"user_id" gorm:"column:user_id;index"`
	ParentCommentID *uint      `json:"parent_comment_id" gorm:"column:parent_comment_id;index"`
	Content         string     `json:"content" gorm:"column:content;type:text"`
	LikeCount       int64      `json:"like_count" gorm:"column:like_count;default:0"`
	IsApproved      bool       `json:"is_approved" gorm:"column:is_approved;default:false;index"`
	CreatedAt       time.Time  `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	Children        []*Comment `gorm:"-" json:"children,omitempty"` // 返回树结构时使用
}

func (Comment) TableName() string { return "comments" }

// ---------- DB ----------

type App struct {
	DB *gorm.DB
}

func mustInitDB() *gorm.DB {
	// 打开外键 + WAL + busy_timeout
	dsn := "blog.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("open sqlite:", err)
	}
	// 连接池建议：SQLite 单写者，避免写锁冲突
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(1 * time.Hour)
	}

	// 自动迁移
	if err := db.AutoMigrate(&Article{}, &Comment{}); err != nil {
		log.Fatal("auto migrate:", err)
	}

	// 保险起见再启一次外键（部分场景下连接串之外的设置也生效）
	db.Exec("PRAGMA foreign_keys = ON;")

	return db
}

// ---------- Helpers ----------

type PageResp[T any] struct {
	List      []T   `json:"list"`
	Page      int   `json:"page"`
	PageSize  int   `json:"page_size"`
	Total     int64 `json:"total"`
	TotalPage int   `json:"total_page"`
}

func parsePage(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 10
	}
	return
}

func isAdmin(c *gin.Context) bool {
	return strings.ToLower(c.GetHeader("X-Admin")) == "true"
}

func respondErr(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"error": msg})
}

func defaultIfEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ---------- Request DTOs ----------

type CreateArticleReq struct {
	Title      string `json:"title" binding:"required"`
	Content    string `json:"content" binding:"required"`
	AuthorID   uint   `json:"author_id" binding:"required"`
	CategoryID uint   `json:"category_id" binding:"required"`
	Status     string `json:"status" binding:"omitempty,oneof=draft published archived"`
}

type UpdateArticleReq struct {
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	AuthorID   *uint   `json:"author_id"`
	CategoryID *uint   `json:"category_id"`
	Status     *string `json:"status" binding:"omitempty,oneof=draft published archived"`
}

type ChangeStatusReq struct {
	Status string `json:"status" binding:"required,oneof=draft published archived"`
}

type CreateCommentReq struct {
	UserID          uint   `json:"user_id" binding:"required"`
	ParentCommentID *uint  `json:"parent_comment_id"`
	Content         string `json:"content" binding:"required"`
}

type ApproveReq struct {
	Approved bool `json:"approved"`
}

// ---------- Handlers: Articles ----------

func (a *App) createArticle(c *gin.Context) {
	var req CreateArticleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, http.StatusBadRequest, err.Error())
		return
	}
	art := &Article{
		Title:      req.Title,
		Content:    req.Content,
		AuthorID:   req.AuthorID,
		CategoryID: req.CategoryID,
		Status:     defaultIfEmpty(req.Status, "draft"),
	}
	if err := a.DB.Create(art).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, art)
}

func (a *App) listArticles(c *gin.Context) {
	page, size := parsePage(c)
	var (
		q          = c.Query("q")
		authorID   = c.Query("author_id")
		categoryID = c.Query("category_id")
		status     = c.Query("status")                     // 可选 draft|published|archived
		sort       = c.DefaultQuery("sort", "-created_at") // -created_at / created_at / -view_count ...
	)

	db := a.DB.Model(&Article{})
	if q != "" {
		db = db.Where("title LIKE ? OR content LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	if authorID != "" {
		db = db.Where("author_id = ?", authorID)
	}
	if categoryID != "" {
		db = db.Where("category_id = ?", categoryID)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}

	if sort != "" {
		order := map[string]string{
			"created_at":  "created_at ASC",
			"-created_at": "created_at DESC",
			"view_count":  "view_count ASC",
			"-view_count": "view_count DESC",
		}[sort]
		if order != "" {
			db = db.Order(order)
		}
	}

	var list []Article
	if err := db.Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	totalPage := int((total + int64(size) - 1) / int64(size))
	c.JSON(http.StatusOK, PageResp[Article]{List: list, Page: page, PageSize: size, Total: total, TotalPage: totalPage})
}

func (a *App) getArticle(c *gin.Context) {
	id := c.Param("id")
	var art Article

	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&art, "sqlid = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Model(&art).UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error; err != nil {
			return err
		}
		return tx.First(&art, "sqlid = ?", id).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondErr(c, http.StatusNotFound, "article not found")
			return
		}
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, art)
}

func (a *App) updateArticle(c *gin.Context) {
	id := c.Param("id")
	var req UpdateArticleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, http.StatusBadRequest, err.Error())
		return
	}
	var art Article
	if err := a.DB.First(&art, "sqlid = ?", id).Error; err != nil {
		respondErr(c, http.StatusNotFound, "article not found")
		return
	}

	updates := map[string]any{}
	if req.Title != nil {
		updates["title"] = *req.Title
		art.Title = *req.Title
	}
	if req.Content != nil {
		updates["content"] = *req.Content
		art.Content = *req.Content
	}
	if req.AuthorID != nil {
		updates["author_id"] = *req.AuthorID
		art.AuthorID = *req.AuthorID
	}
	if req.CategoryID != nil {
		updates["category_id"] = *req.CategoryID
		art.CategoryID = *req.CategoryID
	}
	if req.Status != nil {
		updates["status"] = *req.Status
		art.Status = *req.Status
	}

	if len(updates) > 0 {
		if err := a.DB.Model(&art).Updates(updates).Error; err != nil {
			respondErr(c, http.StatusInternalServerError, err.Error())
			return
		}
	}
	c.JSON(http.StatusOK, art) // 保证返回最新
}

func (a *App) changeStatus(c *gin.Context) {
	id := c.Param("id")
	var req ChangeStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Status != "draft" && req.Status != "published" && req.Status != "archived" {
		respondErr(c, http.StatusBadRequest, "invalid status")
		return
	}
	if err := a.DB.Model(&Article{}).Where("sqlid = ?", id).Update("status", req.Status).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "status": req.Status})
}

func (a *App) deleteArticle(c *gin.Context) {
	id := c.Param("id")
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("sqlarticle_id = ?", id).Delete(&Comment{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&Article{}, "sqlid = ?", id).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- Handlers: Comments ----------

func (a *App) listCommentsTree(c *gin.Context) {
	articleID := c.Param("id")
	includeUnapproved := c.Query("include_unapproved") == "1"
	var cs []Comment

	db := a.DB.Where("sqlarticle_id = ?", articleID)
	if !includeUnapproved {
		db = db.Where("is_approved = ?", true)
	}
	if err := db.Order("created_at ASC").Find(&cs).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	tree := buildCommentTree(cs)
	c.JSON(http.StatusOK, tree)
}

// 修复：不要取 for 循环变量地址；转为稳定节点映射再组树
func buildCommentTree(all []Comment) []*Comment {
	id2node := make(map[uint]*Comment, len(all))
	var roots []*Comment

	// 先为每个元素分配独立节点地址
	for i := range all {
		c := all[i] // 拷贝值
		node := c   // 新变量，地址稳定
		id2node[c.ID] = &node
	}

	// 再挂接父子关系
	for i := range all {
		c := all[i]
		n := id2node[c.ID]
		if c.ParentCommentID == nil {
			roots = append(roots, n)
			continue
		}
		if p, ok := id2node[*c.ParentCommentID]; ok {
			p.Children = append(p.Children, n)
		} else {
			// 若找不到父节点（脏数据），当作根节点兜底
			roots = append(roots, n)
		}
	}
	return roots
}

func (a *App) createComment(c *gin.Context) {
	articleID, _ := strconv.Atoi(c.Param("id"))
	var req CreateCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, http.StatusBadRequest, err.Error())
		return
	}
	var cnt int64
	a.DB.Model(&Article{}).Where("sqlid = ?", articleID).Count(&cnt)
	if cnt == 0 {
		respondErr(c, http.StatusNotFound, "article not found")
		return
	}
	if req.ParentCommentID != nil {
		var pcnt int64
		a.DB.Model(&Comment{}).
			Where("id = ? AND sqlarticle_id = ?", *req.ParentCommentID, articleID).
			Count(&pcnt)
		if pcnt == 0 {
			respondErr(c, http.StatusBadRequest, "parent_comment not found or not under this article")
			return
		}
	}
	cm := &Comment{
		SQLArticleID:    uint(articleID),
		UserID:          req.UserID,
		ParentCommentID: req.ParentCommentID,
		Content:         strings.TrimSpace(req.Content),
	}
	if cm.Content == "" {
		respondErr(c, http.StatusBadRequest, "empty content")
		return
	}
	if err := a.DB.Create(cm).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, cm)
}

func (a *App) likeComment(c *gin.Context) {
	id := c.Param("id")
	res := a.DB.Model(&Comment{}).Where("id = ?", id).
		UpdateColumn("like_count", gorm.Expr("like_count + 1"))
	if res.Error != nil {
		respondErr(c, http.StatusInternalServerError, res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		respondErr(c, http.StatusNotFound, "comment not found")
		return
	}
	var out Comment
	a.DB.First(&out, "id = ?", id)
	c.JSON(http.StatusOK, out)
}

func (a *App) approveComment(c *gin.Context) {
	if !isAdmin(c) {
		respondErr(c, http.StatusForbidden, "admin only")
		return
	}
	id := c.Param("id")
	var req ApproveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, http.StatusBadRequest, err.Error())
		return
	}
	res := a.DB.Model(&Comment{}).Where("id = ?", id).Update("is_approved", req.Approved)
	if res.Error != nil {
		respondErr(c, http.StatusInternalServerError, res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		respondErr(c, http.StatusNotFound, "comment not found")
		return
	}
	var out Comment
	a.DB.First(&out, "id = ?", id)
	c.JSON(http.StatusOK, out)
}

func (a *App) deleteComment(c *gin.Context) {
	if !isAdmin(c) {
		respondErr(c, http.StatusForbidden, "admin only")
		return
	}
	id := c.Param("id")
	res := a.DB.Delete(&Comment{}, "id = ?", id)
	if res.Error != nil {
		respondErr(c, http.StatusInternalServerError, res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		respondErr(c, http.StatusNotFound, "comment not found")
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- Handlers: Stats & Misc ----------

func (a *App) topArticles(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit < 1 || limit > 100 {
		limit = 10
	}
	var list []Article
	if err := a.DB.Order("view_count DESC").Limit(limit).Find(&list).Error; err != nil {
		respondErr(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, list)
}

func health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "ts": time.Now().UTC()})
}

// ---------- Main ----------

func main() {
	db := mustInitDB()
	app := &App{DB: db}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// CORS：允许任意源（生产请白名单化）
	r.Use(cors.New(cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Admin"},
		ExposeHeaders: []string{"Content-Length"},
		MaxAge:        12 * time.Hour,
	}))

	api := r.Group("/api")
	{
		api.GET("/health", health)

		// Articles
		api.POST("/articles", app.createArticle)
		api.GET("/articles", app.listArticles)
		api.GET("/articles/:id", app.getArticle)
		api.PUT("/articles/:id", app.updateArticle)
		api.PATCH("/articles/:id/status", app.changeStatus)
		api.DELETE("/articles/:id", app.deleteArticle)
		api.GET("/stats/top", app.topArticles)

		// Comments
		api.GET("/articles/:id/comments", app.listCommentsTree)
		api.POST("/articles/:id/comments", app.createComment)
		api.PATCH("/comments/:id/like", app.likeComment)
		api.PATCH("/comments/:id/approve", app.approveComment)
		api.DELETE("/comments/:id", app.deleteComment)
	}

	log.Println("Blog API listening on http://localhost:8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
