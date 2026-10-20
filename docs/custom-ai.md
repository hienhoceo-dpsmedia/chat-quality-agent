# Cấu hình Custom AI

Vào **Cài đặt → Cấu hình AI**, chọn **Custom AI — OpenAI-compatible**.

1. **Model ID:** nhập tên model đúng với máy chủ, hoặc bấm làm mới để lấy danh sách từ `/models`.
   Có thể nhập tự do, ví dụ `gpt-5.6-sol` hoặc `vendor/model:latest`.
2. **Endpoint / Base URL:** dán URL đầy đủ kết thúc bằng `/v1/chat/completions`, hoặc base URL
   kết thúc bằng `/v1`. Ứng dụng không nối trùng `/chat/completions`.
3. **API key:** tùy chọn. Khi không có key, request không gửi header Authorization.
   Key đã lưu hiển thị bằng dấu chấm; giữ dấu chấm để giữ key, xóa nội dung để bỏ key.
4. **Thời gian chờ / Timeout:** số nguyên từ **5–600 giây**, mặc định **120 giây**.
   Áp dụng cho cả lượt kiểm tra và lượt phân tích thực tế, kể cả batch; bao gồm toàn bộ thời gian retry.
5. Bấm **Kiểm tra kết nối** rồi **Lưu cài đặt** khi đã chọn cấu hình muốn dùng.

Kiểm tra và làm mới model dùng cấu hình đang nhập; không tự lưu thay đổi. Discovery model có
timeout riêng 20 giây. Nếu máy chủ không hỗ trợ `/models`, vẫn có thể nhập model trực tiếp.
Đổi nhà cung cấp hoặc endpoint không tự dùng lại key đã lưu cho máy chủ cũ.

## Đã kiểm chứng local ngày 20/10/2026

- Endpoint do người dùng cung cấp: `/v1/models` hoạt động không cần API key.
- Model `gpt-5.6-sol` trả completion `OK`; lượt kiểm tra qua CQA mất khoảng 38 giây.
- API lưu/đọc lại timeout 300 giây, cập nhật 120 giây; giá trị 601 bị từ chối mà không thay đổi cấu hình.
- Kiểm tra saved-config keyless qua CQA trả HTTP 200; tenant kiểm thử được dọn sau khi hoàn tất.
- Cấu hình DPS.MEDIA không bị ghi đè trong smoke test; người dùng tự lưu model/timeout muốn dùng.

Local proxy đã được cấu hình chờ 620 giây để hỗ trợ timeout tối đa. Khi triển khai ở proxy khác,
đặt read timeout lớn hơn timeout Custom AI đã chọn.
