# Triển khai CQA cho DPS trên Portainer + Nginx Proxy Manager

Runbook dành cho **Docker standalone**, Portainer và Nginx Proxy Manager (NPM) đang có trên VPS.
Domain **đề xuất**, chưa triển khai: `https://cqa.dps.media/`.
File triển khai: `docker-compose.portainer.yml`; biến mẫu: `.env.production.example`.

## 1. Kiến trúc và điều kiện

| Thành phần | Địa chỉ / kết nối |
| --- | --- |
| CQA mới | NPM → HTTP `cqa_app:8080` |
| Chatwoot production | `https://chat.dpsmedia.vn/` |
| CRM đang có | `https://dashboard.dps.media/` |
| ZCA Bridge đang có | `https://zca-cw.dpsmedia.vn/` |
| Proxy network có sẵn | `nginx-proxy-manager_default` |
| MySQL riêng | Service `cqa-db`, container `cqa_db`, chỉ trên network DB nội bộ |

App tham gia proxy network và DB network; MySQL chỉ tham gia DB network. Không publish host port,
không cài thêm nginx/ACME, không chiếm cổng 80/443. NPM hiện hữu kết thúc TLS.
Volumes cố định `dps_cqa_mysql_data` và `dps_cqa_files`, độc lập dữ liệu local.
CQA lấy hội thoại từ Chatwoot; **không cần kết nối trực tiếp Zalo/ZCA**.

Trên VPS, kiểm tra Docker engine mà Portainer endpoint đang quản lý, dung lượng trống và network:

```bash
docker context show
docker info --format '{{.Name}}'
docker network inspect nginx-proxy-manager_default
```

NPM phải nối vào network này. Các lệnh SSH bên dưới phải chạy trên **đúng engine đó**
(build trên laptop hoặc engine khác không làm image xuất hiện trong Portainer).

## 2. Chuẩn bị source và build image qua SSH

Nhánh DPS cần dùng là **`feat/p0-chatwoot-sync`**, không mặc định `main`.
Kiểm tra revision từ nhánh này có đủ adapter Chatwoot, Custom AI, timeout và file triển khai trước
khi build. Ghi lại revision/image tag để có thể cập nhật hoặc rollback đúng bản.

Tại thư mục triển khai đã chọn trên VPS:

```bash
git clone --branch feat/p0-chatwoot-sync --single-branch \
  https://github.com/hienhoceo-dpsmedia/chat-quality-agent.git
cd chat-quality-agent
git branch --show-current
git status --short
git log -1 --oneline
# Xác nhận revision có Custom AI và các file deployment trước khi build.
git ls-files backend/ai/custom.go docs/custom-ai.md docker-compose.portainer.yml
REV=$(git rev-parse --short HEAD)
docker build -t "dps-cqa:$REV" .
docker image inspect "dps-cqa:$REV" --format '{{.Id}}'
```

Ghi lại full revision (`git rev-parse HEAD`), image ID và tag `dps-cqa:<gitsha>`.
Build phải từ working tree sạch, đúng revision đã duyệt để tag phản ánh source thực tế.
Portainer Web editor không build source; Compose này cố ý chỉ nhận `image`, không có `build`.
Không dùng `buitanviet/chat-quality-agent`: image upstream không chứa tính năng DPS Custom AI.
Không bật tùy chọn Portainer **Always pull image** cho tag local này.

## 3. Tạo biến production và stack

Sinh riêng từng secret bằng `openssl` trên máy tin cậy và lưu trong nơi quản lý bí mật:

- `DB_PASSWORD`: `openssl rand -hex 32`.
- `MYSQL_ROOT_PASSWORD`: chạy riêng `openssl rand -hex 32`, khác mật khẩu app.
- `JWT_SECRET`: `openssl rand -hex 32` → 64 ký tự ASCII.
- `ENCRYPTION_KEY`: `openssl rand -hex 16` → **đúng 32 byte ASCII**, không phải hex 64 ký tự.

Không dùng mật khẩu/token local. Giữ nguyên `ENCRYPTION_KEY` trong mọi cập nhật và khôi phục:
đổi key sẽ làm credentials đã mã hóa không đọc được. Giữ an toàn cả secret và backup.
Mật khẩu DB trong environment chỉ khởi tạo user khi volume trống; sửa biến không tự đổi mật khẩu
trong MySQL đã có dữ liệu. Không tự đổi các giá trị này trong một lần update image.

Portainer → đúng Environment → **Stacks → Add stack → tên `cqa` → Web editor**:

1. Dán toàn bộ `docker-compose.portainer.yml`.
2. Nhập các biến từ `.env.production.example` ở **Environment variables** của stack:
   `CQA_IMAGE=dps-cqa:<gitsha>` vừa build, `APP_URL=https://cqa.dps.media`,
   `DB_NAME=cqa`, `DB_USER=cqa`, bốn secret bắt buộc phía trên; rate limit tùy chọn.
3. Không nhập đường dẫn `env_file` trên máy phát triển; Web editor không có file đó.
4. Deploy stack sau khi xác nhận đủ biến. Compose sẽ từ chối biến bắt buộc trống.
5. Theo dõi `cqa_db` healthy rồi `cqa_app` healthy; DB mới có thể cần vài phút khởi tạo.

App chỉ nhận các biến được liệt kê tường minh; `MYSQL_ROOT_PASSWORD` chỉ truyền vào DB.
Healthcheck DB thực hiện **SELECT 1 qua TCP có xác thực**, không dùng `mysqladmin ping`
(ping có thể báo thành công ngay cả khi xác thực thất bại).

## 4. DNS và Proxy Host trên NPM

1. Tạo DNS **A `cqa.dps.media` → IPv4 VPS**; chỉ tạo AAAA nếu IPv6 thực sự phục vụ VPS.
2. NPM → Proxy Hosts → Add:
   - Domain: `cqa.dps.media`.
   - Scheme: **http**; Forward hostname: **`cqa_app`**; Forward port: **8080**.
   - Bật **Websockets Support**.
   - Advanced:

   ```nginx
   proxy_read_timeout 620s;
   proxy_send_timeout 620s;
   ```

3. SSL → yêu cầu certificate Let's Encrypt, bật **Force SSL**; lưu và kiểm tra certificate.
4. Mở `https://cqa.dps.media/health`, kỳ vọng HTTP 200; sau đó mở giao diện chính.

Không trỏ tới `localhost:8080` trong NPM: localhost ở đây là container NPM.

## 5. Khởi tạo owner, tenant và kênh Chatwoot

DB mới sẽ hiện trang Setup. Người dùng tự đặt email/tên và **mật khẩu owner production mới**;
không có tài khoản/mật khẩu local mặc định. Tạo tenant DPS.MEDIA sau khi đăng nhập.

Trong **Chatwoot production**, tạo hoặc chọn tài khoản agent chuyên dùng cho CQA, thêm agent vào
các inbox cần đọc, lấy API access token từ profile. Tạo inbox production nếu chưa có kênh cần dùng.
Xác định **account ID và inbox ID thật trên production**, không sao chép account `2` hay token local.
Có thể xem account trên URL Chatwoot và liệt kê inbox bằng API dưới đây.
App gọi HTTPS Chatwoot qua outbound của Docker trên proxy network; cần DNS/egress hoạt động.

Ví dụ Bash dùng `curl` và `jq` trên VPS. Các biến bí mật nhập từ nơi quản lý secret, không hardcode
vào tài liệu hoặc bật shell tracing; `JWT` là access token CQA sau đăng nhập, không phải Chatwoot token:

```bash
CQA_URL=https://cqa.dps.media
CW_URL=https://chat.dpsmedia.vn
# Cung cấp JWT, CW_TOKEN, CW_ACCOUNT_ID và CW_INBOX_ID đúng production trước khi chạy.
curl --fail-with-body -sS "$CW_URL/api/v1/accounts/$CW_ACCOUNT_ID/inboxes" \
  -H "api_access_token: $CW_TOKEN" | jq '.payload[] | {id,name}'

# Nếu đã tạo tenant qua UI, lấy đúng id từ GET /api/v1/tenants thay vì tạo lại.
TENANT_ID=$(curl --fail-with-body -sS "$CQA_URL/api/v1/tenants" \
  -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' \
  -d '{"name":"DPS.MEDIA","slug":"dps-media"}' | jq -er '.id')

CHANNEL_ID=$(jq -n --arg token "$CW_TOKEN" \
  --argjson account "$CW_ACCOUNT_ID" --argjson inbox "$CW_INBOX_ID" \
  '{channel_type:"chatwoot",name:"Chatwoot DPS Production",credentials:{
    base_url:"https://chat.dpsmedia.vn",account_id:$account,
    api_token:$token,inbox_ids:[$inbox]}}' | \
  curl --fail-with-body -sS "$CQA_URL/api/v1/tenants/$TENANT_ID/channels" \
    -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' \
    --data-binary @- | jq -er '.id')

curl --fail-with-body -sS -X POST \
  "$CQA_URL/api/v1/tenants/$TENANT_ID/channels/$CHANNEL_ID/sync" \
  -H "Authorization: Bearer $JWT"
```

`credentials` phải là **JSON object**, không phải chuỗi JSON. Tenant nằm trong đường dẫn API.
Đồng bộ hiện lấy cửa sổ **7 ngày gần nhất**, không phải backfill toàn bộ lịch sử.
Handler tạo channel có kiểm tra kết nối Chatwoot; kiểm tra lịch sử sync, hội thoại/tin nhắn và inbox
đúng trên UI sau khi chạy. Không dùng endpoint `/channels/{id}/test` làm bằng chứng kết nối:
endpoint đó còn là placeholder ở upstream.

### Nếu Chatwoot trả 401 qua proxy

Adapter hiện gửi header chuẩn Chatwoot **`api_access_token`** (dấu gạch dưới).
Nginx có thể loại header này trước khi tới Chatwoot. Trước hết kiểm tra token, quyền agent và account ID.
Nếu token đúng nhưng qua NPM vẫn 401, tại **Proxy Host của `chat.dpsmedia.vn`** thêm directive
ở server context qua Advanced:

```nginx
underscores_in_headers on;
```

Lưu để NPM nạp lại cấu hình, kiểm tra cấu hình hợp lệ rồi thử lại cùng lệnh `/inboxes`.
Directive này cần ở proxy nhận request Chatwoot, không chỉ ở host CQA. Nếu còn proxy trung gian,
kiểm tra nó có giữ header không. Không đổi header sang tên có dấu gạch ngang tùy tiện.

## 6. Cấu hình Custom AI và kiểm tra đường mạng Tailscale

Trong tenant → Cài đặt → Cấu hình AI → **Custom AI — OpenAI-compatible**:

- Endpoint: `https://desktop-0153vfv.tail738706.ts.net/v1/chat/completions`.
- Model: nhập model server hỗ trợ, ví dụ `gpt-5.6-sol`, hoặc làm mới danh sách.
- API key: theo cấu hình server; để trống nếu server cho phép keyless.
- Timeout: mặc định **120 giây**, tùy chọn số nguyên **5–600 giây**.
- Bấm **Kiểm tra kết nối** rồi **Lưu cài đặt**.

Nếu endpoint `*.ts.net` này chỉ được mở trong tailnet, VPS cần quyền Tailscale/tailnet và container app
cần DNS, route, firewall/ACL cho phép truy cập. Tên miền `*.ts.net` tự nó không xác nhận endpoint
riêng hay đã được publish qua Funnel; kiểm tra theo cấu hình máy chủ thực tế. VPS truy cập được chưa đủ
nếu Docker forwarding/DNS chặn container. Thành công local không chứng minh production truy cập được.

Kiểm tra từ VPS và **từ chính app container** (thêm Authorization theo server nếu cần):

```bash
curl --fail-with-body --max-time 20 \
  https://desktop-0153vfv.tail738706.ts.net/v1/models
docker exec cqa_app wget -T 20 -qO- \
  https://desktop-0153vfv.tail738706.ts.net/v1/models
```

Nếu `/models` không được server hỗ trợ, nhập model thủ công và kiểm tra completion trong CQA.
Phân biệt lỗi DNS/timeout/ACL với 401 hoặc model không tồn tại; kiểm tra log trước khi sửa cấu hình.
Chi tiết hành vi lưu key, timeout và kiểm chứng **local**: [Custom AI](custom-ai.md).
Phân tích hội thoại AI tổng quát upstream dùng được; **P1 chấm riêng từng agent** và **P2 nhúng CRM/SSO**
chưa được triển khai. Không coi việc deploy này là đã có đầy đủ báo cáo chuyên biệt đó.

## 7. Kiểm tra sau triển khai và xử lý lỗi

```bash
docker inspect cqa_db --format '{{.State.Health.Status}}'
docker inspect cqa_app --format '{{.State.Health.Status}}'
docker logs --tail 100 cqa_db
docker logs --tail 100 cqa_app
curl --fail-with-body -sS https://cqa.dps.media/health
```

Sau healthcheck: đăng nhập, kiểm tra tenant, test kênh, sync 7 ngày, test Custom AI và chạy một lượt
phân tích hội thoại thật. Không đưa secrets trong log/ticket. Health app chỉ xác nhận HTTP sống,
không thay thế việc thử Chatwoot và AI end-to-end. Lỗi 502: xem log app/NPM và network alias;
DB chưa healthy: xem log MySQL và thông tin user/password đã lưu trong volume.
Tài liệu này **chưa được kiểm chứng trên VPS production**.

## 8. Backup, cập nhật và rollback

Trước update: lưu revision/image tag cũ, cấu hình stack và secrets ở nơi an toàn. Tạo backup DB:

```bash
# Chạy trong thư mục backup có sẵn, quyền hạn chế; chọn tên file mới cho mỗi lần.
docker exec cqa_db sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump -uroot --single-transaction --routines --triggers --events --no-tablespaces "$MYSQL_DATABASE"' > cqa-before-update.sql
```

Kiểm tra exit code và backup có nội dung hợp lệ. Sao lưu cả volume `dps_cqa_files` bằng công cụ
backup volume hiện có trong cửa sổ dừng ghi nếu cần tính nhất quán; lưu cùng DB và encryption key.
Thử phục hồi trên môi trường riêng trước khi cần dùng backup thực tế.

Cập nhật: tại clone sạch trên VPS, fetch nhánh `feat/p0-chatwoot-sync`, chọn revision đã duyệt,
build tag **mới** `dps-cqa:<gitsha>` như mục 2. Sửa duy nhất `CQA_IMAGE` trong biến stack Portainer,
Update stack, giữ volumes và secrets. Giữ image cũ để rollback, không dùng tag `latest` trôi nổi.
Rollback: đặt lại tag cũ và update stack. Nếu revision mới đã migration schema không tương thích,
cần phục hồi DB/files từ backup phù hợp trong cửa sổ bảo trì; đổi image đơn thuần có thể chưa đủ.
Không chạy `down -v`, không xóa volumes, không rotate `ENCRYPTION_KEY` trong update/rollback.

### Tùy chọn CLI thay cho Web editor

Chỉ dùng trên VPS đúng engine, và chọn một cách quản lý stack thống nhất với Portainer.
Sao chép `.env.production.example` thành `.env.production`, điền giá trị thật, hạn chế quyền đọc
(`chmod 600 .env.production`). File này đã được gitignore. Chạy từ thư mục repo:

```bash
docker compose --env-file .env.production -p cqa -f docker-compose.portainer.yml config --quiet
docker compose --env-file .env.production -p cqa -f docker-compose.portainer.yml up -d
```

Không chạy các lệnh triển khai này trên workspace local. Dùng `config --quiet` để kiểm tra cú pháp,
không in cấu hình đã resolve vì chứa secrets. DNS, quyền VPS, source đã phát hành và mạng Tailscale
là các đầu việc cần hoàn tất khi triển khai thực tế.
