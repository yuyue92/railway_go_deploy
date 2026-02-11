# Blog API (Go + Gin + GORM + SQLite)

这是一个轻量级 **博客系统后端 API**，基于 **Gin** 框架、**GORM** ORM，并使用 **SQLite** 作为本地数据库（自动建库/迁移）。

## 功能概览

- ✅ 文章（Article）CRUD
- ✅ 文章状态流转：`draft | published | archived`
- ✅ 文章浏览量统计：获取文章详情时自动 `view_count + 1`
- ✅ 文章列表检索：关键字、作者、分类、状态过滤 + 分页 + 排序
- ✅ Top 热门文章：按浏览量排序
- ✅ 评论（Comment）系统
  - 树形评论（支持父子评论）
  - 点赞：`like_count + 1`
  - 审核：管理员可设置 `is_approved`
  - 默认仅返回已审核评论（可选返回全部）

---

## 技术栈

- Go
- Gin (`github.com/gin-gonic/gin`)
- CORS (`github.com/gin-contrib/cors`)
- GORM (`gorm.io/gorm`)
- SQLite Driver (`github.com/glebarez/sqlite`)

---

## 运行环境要求

- Go 1.20+（建议）
- 无需单独安装数据库（SQLite 会生成本地文件）

---

## 快速开始

### 1) 安装依赖

```bash
go mod tidy
```

### 2) 启动服务

```bash
go run main.go
```

启动后默认监听：

- `http://localhost:8080`

服务启动时会自动创建/迁移数据库文件：

- `blog.db`

---

## 数据库说明

代码中使用的 SQLite DSN：

- `blog.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`

同时还做了连接池限制（SQLite 单写者，避免写锁冲突）：

- `MaxOpenConns = 1`
- `MaxIdleConns = 1`

---

## API 约定

- **所有接口前缀：** `/api`
- **Content-Type：** `application/json`
- **CORS：** 允许任意源（生产环境建议改为白名单）
- **管理员鉴权：** 使用请求头 `X-Admin: true`
  - 当前代码里仅用于：审核评论、删除评论

---

## 接口列表

### Health

- `GET /api/health`

```bash
curl http://localhost:8080/api/health
```

---

## Articles

### 1) 创建文章

- `POST /api/articles`

请求体（示例）：

```json
{
  "title": "Hello",
  "content": "My first post",
  "author_id": 1,
  "category_id": 10,
  "status": "draft"
}
```

```bash
curl -X POST http://localhost:8080/api/articles   -H "Content-Type: application/json"   -d '{
    "title":"Hello",
    "content":"My first post",
    "author_id":1,
    "category_id":10,
    "status":"draft"
  }'
```

> `status` 为空时默认 `draft`

---

### 2) 文章列表（过滤 / 分页 / 排序）

- `GET /api/articles`

Query 参数：

- `page`：页码（默认 1）
- `page_size`：每页数量（默认 10，最大 100）
- `q`：全文关键字（匹配 title 或 content）
- `author_id`：作者 ID
- `category_id`：分类 ID
- `status`：`draft | published | archived`
- `sort`：默认 `-created_at`
  - 支持：`created_at` / `-created_at` / `view_count` / `-view_count`

示例：

```bash
curl "http://localhost:8080/api/articles?page=1&page_size=10&status=published&sort=-view_count&q=golang"
```

返回结构（分页）：

```json
{
  "list": [],
  "page": 1,
  "page_size": 10,
  "total": 0,
  "total_page": 0
}
```

---

### 3) 获取文章详情（会自动 +1 浏览量）

- `GET /api/articles/:id`

```bash
curl http://localhost:8080/api/articles/1
```

> 该接口在事务中执行：先查询文章，再 `view_count + 1`，再返回最新文章数据。

---

### 4) 更新文章（字段可选更新）

- `PUT /api/articles/:id`

请求体（字段指针可空；示例）：

```json
{
  "title": "New title",
  "content": "Updated content",
  "status": "published"
}
```

```bash
curl -X PUT http://localhost:8080/api/articles/1   -H "Content-Type: application/json"   -d '{"title":"New title","content":"Updated content","status":"published"}'
```

---

### 5) 修改文章状态

- `PATCH /api/articles/:id/status`

请求体：

```json
{ "status": "published" }
```

```bash
curl -X PATCH http://localhost:8080/api/articles/1/status   -H "Content-Type: application/json"   -d '{"status":"published"}'
```

> status 仅允许：`draft / published / archived`，否则 400。

---

### 6) 删除文章（会先删该文章下的评论）

- `DELETE /api/articles/:id`

```bash
curl -X DELETE http://localhost:8080/api/articles/1
```

> 代码中在事务里：先删 `Comment`（sqlarticle_id=文章ID），再删 `Article`。

---

### 7) Top 热门文章

- `GET /api/stats/top?limit=10`

```bash
curl "http://localhost:8080/api/stats/top?limit=10"
```

> `limit` 范围：1~100，默认 10。

---

## Comments

### 1) 获取文章评论（树形结构）

- `GET /api/articles/:id/comments`

默认只返回已审核评论（`is_approved=true`）。

可选返回全部评论（包含未审核）：

- `include_unapproved=1`

```bash
curl "http://localhost:8080/api/articles/1/comments"
curl "http://localhost:8080/api/articles/1/comments?include_unapproved=1"
```

返回示例（树形）：

```json
[
  {
    "id": 1,
    "article_id": 1,
    "user_id": 2,
    "content": "root",
    "children": [
      { "id": 2, "parent_comment_id": 1, "content": "reply" }
    ]
  }
]
```

---

### 2) 创建评论（支持回复）

- `POST /api/articles/:id/comments`

请求体示例：

```json
{
  "user_id": 2,
  "content": "Nice post!",
  "parent_comment_id": 1
}
```

```bash
curl -X POST http://localhost:8080/api/articles/1/comments   -H "Content-Type: application/json"   -d '{"user_id":2,"content":"Nice post!"}'
```

回复评论（parent_comment_id 必须属于同一文章）：

```bash
curl -X POST http://localhost:8080/api/articles/1/comments   -H "Content-Type: application/json"   -d '{"user_id":2,"content":"reply","parent_comment_id":1}'
```

---

### 3) 评论点赞

- `PATCH /api/comments/:id/like`

```bash
curl -X PATCH http://localhost:8080/api/comments/1/like
```

> 原子更新：`like_count = like_count + 1`

---

### 4) 审核评论（管理员）

- `PATCH /api/comments/:id/approve`
- Header：`X-Admin: true`

请求体：

```json
{ "approved": true }
```

```bash
curl -X PATCH http://localhost:8080/api/comments/1/approve   -H "Content-Type: application/json"   -H "X-Admin: true"   -d '{"approved":true}'
```

---

### 5) 删除评论（管理员）

- `DELETE /api/comments/:id`
- Header：`X-Admin: true`

```bash
curl -X DELETE http://localhost:8080/api/comments/1   -H "X-Admin: true"
```

---

## 数据模型

### Article

- `id`（sqlid）
- `title`
- `content`
- `author_id`
- `category_id`
- `status`：`draft | published | archived`
- `view_count`
- `created_at`
- `updated_at`

### Comment

- `id`
- `article_id`（sqlarticle_id）
- `user_id`
- `parent_comment_id`（可空）
- `content`
- `like_count`
- `is_approved`
- `created_at`
- `children`（仅返回树形时使用，非数据库字段）

---

## 开发/调试建议

- 生产环境建议：
  - 将 CORS 改为白名单域名
  - 为管理员操作加真实鉴权（JWT/Session）
  - 给状态/字段做更强校验（例如标题不能为空等）
  - 给列表接口增加更细的排序白名单/字段映射

---

## License

MIT
