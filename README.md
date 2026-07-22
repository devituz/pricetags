# Pricetags — do'kondagi elektron narx ko'rsatkichlari servisi

Go 1.21+ (sqlx, Fiber), PostgreSQL 16, Elasticsearch 8, golang-migrate.

## Ishga tushirish

```bash
cp .env.example .env   # ixtiyoriy — defaultlar bilan ham ishlayveradi
docker compose up -d --build
```

Shu bilan hammasi tayyor: Postgres (host: `5433`), Elasticsearch (`9200`) va servis (`8080`) ko'tariladi.
Portlar va parollar `.env` orqali o'zgartiriladi (`.env.example` ga qarang); `.env` git'ga kirmaydi.

**Swagger UI: [http://localhost:8080/docs](http://localhost:8080/docs)** — barcha endpointlarni brauzerdan tekshirish mumkin.
*Authorize* tugmasi orqali `X-Company-Id` kiritiladi (masalan `11111111-1111-1111-1111-111111111111`), spec: `/openapi.yaml`.
Migratsiyalar binary ichiga embed qilingan va start paytida avtomatik qo'llanadi (`MIGRATE_ON_START=true`).
CLI orqali ham mumkin:

```bash
make migrate-up      # golang-migrate CLI kerak
make test            # unit testlar
make run             # lokal ishga tushirish (Postgres/ES compose'dan)
```

Har bir so'rovda `X-Company-Id: <uuid>` header majburiy.

## API

```bash
CID="11111111-1111-1111-1111-111111111111"

# 1) Bulk upsert (1000 tagacha, (company_id, sku) bo'yicha)
curl -X POST localhost:8080/products/bulk -H "X-Company-Id: $CID" -H 'Content-Type: application/json' -d '{
  "products": [
    {"name":"Coca-Cola 1L","sku":"COLA-1","barcode":["478001","478002"],"supply_price":8000,"retail_price":11000}
  ]}'
# => {"created":1,"updated":0,"total":1}

# 2) Slot biriktirish (product_id: null — slotni bo'shatish/oldindan yaratish)
curl -X PUT localhost:8080/slots -H "X-Company-Id: $CID" -H 'Content-Type: application/json' \
  -d '[{"slot":1,"product_id":"<uuid>"},{"slot":2,"product_id":null}]'

# 3) Doska: pagination + search (nom, sku yoki slot raqami)
curl "localhost:8080/slots?page=1&limit=20&search=cola" -H "X-Company-Id: $CID"

# 4) Soft delete (slotlari bo'shaydi, ES'dan o'chadi)
curl -X DELETE localhost:8080/products -H "X-Company-Id: $CID" -H 'Content-Type: application/json' \
  -d '{"ids":["<uuid>"]}'

# 5) Hisobot (bitta SQL)
curl localhost:8080/reports/stock-value -H "X-Company-Id: $CID"
# => {"total_supply_value":22200,"occupied_slots":3,"empty_slots":1}

# 6) Elastic qidiruv: fuzzy nom, sku, barcode, slot raqami
curl "localhost:8080/products/search?q=cofa" -H "X-Company-Id: $CID"
```

Xatolar: `400` — validatsiya (`{"error":"products[3]: sku is required"}`), `500` — ichki xato (detali logda).

## Sxema va indekslar

**product**: `id uuid PK`, `company_id`, `name`, `sku`, `barcode text[]` (bir mahsulotda bir nechta bo'lgani uchun massiv), `supply_price/retail_price numeric(14,2)` (pul uchun float emas), `created_at`, `updated_at`, `deleted_at` (soft delete).

**shelf_slot**: `PK (company_id, slot_number)`, `product_id uuid NULL REFERENCES product`, `CHECK (slot_number >= 1)`.

| Indeks | Nima uchun |
|---|---|
| `product_company_sku_uq` — UNIQUE `(company_id, sku) WHERE deleted_at IS NULL` | Bulk upsert'ning `ON CONFLICT` nishoni. **Partial** bo'lgani sabab soft delete qilingan sku'ni qayta yaratish mumkin — o'chirilgan yozuv unique'ni band qilib turmaydi. |
| `product_name_trgm_idx`, `product_sku_trgm_idx` — GIN `gin_trgm_ops` | `GET /slots?search=` `ILIKE '%q%'` infix qidiradi, btree bunga yaramaydi, trigram GIN yaraydi (pastdagi EXPLAIN'ga qarang). |
| `shelf_slot_pkey (company_id, slot_number)` | Doska har doim kompaniya kesimida slot tartibida o'qiladi — PK'ning o'zi listing'ni yopadi; slot raqami kompaniya ichida takrorlanmasligini ham shu kafolatlaydi. |
| `shelf_slot_product_idx` — `(product_id) WHERE product_id IS NOT NULL` | `DELETE /products` da mahsulot ro'yxati bo'yicha slotlarni bo'shatish; bo'sh slotlar indeksga kirmaydi. |

## Qabul qilingan qarorlar

**Doska modeli.** Slot yozuvi birinchi biriktirishda yaratiladi va keyin o'chirilmaydi: bo'shatilganda `product_id = NULL` bo'lib qoladi, shuning uchun foydalanuvchi butun doskani (bo'sh slotlar bilan) ko'radi. Doskani oldindan chizish kerak bo'lsa — `PUT /slots` ga `product_id: null` bilan ro'yxat yuboriladi. Slotda mahsulot ma'lumoti nusxalanmaydi, `GET /slots` join bilan oladi.

**Bulk upsert va bitta request ichidagi dublikat sku.** Postgres multi-row `ON CONFLICT` bitta yozuvni ikki marta yangilay olmaydi ("cannot affect row a second time"). Shuning uchun request avval Go'da dedupe qilinadi — oxirgi kelgan sku g'olib (`domain.DedupeBySKU`, testlangan). Keyin butun batch **bitta** `INSERT ... SELECT FROM jsonb_to_recordset ... ON CONFLICT DO UPDATE` bo'lib ketadi — 1000 element uchun bitta round-trip. `(xmax = 0)` orqali created/updated ajratiladi.

**Tenant izolyatsiyasi.** `company_id` faqat middleware'da UUID sifatida parse qilinadi va har bir SQL/ES so'rovda majburiy filtr: Postgres'da WHERE'da, Elastic'da `bool.filter.term` (should emas — test bilan qotirilgan). Slot biriktirishda product_id boshqa kompaniyaniki bo'lsa — 400.

**Slot biriktirish va concurrency.** `PUT /slots` bitta transaksiyada: (1) tegilayotgan slotlar `ORDER BY slot_number ... FOR UPDATE` bilan qulflanadi — ikki parallel request slotlarni bir xil tartibda olgani uchun deadlock bo'lmaydi, ikkinchisi kutib turadi va oxirgisi g'olib bo'ladi; (2) eski egalar (displaced) eslab qolinadi; (3) `ON CONFLICT (company_id, slot_number) DO UPDATE` bilan yoziladi. 30 ta parallel request bilan smoke-test qilingan — hammasi 200, holat konsistent.

**Elastic sync.** Postgres — source of truth, ES — qidiruv proyeksiyasi, sync DB commit'dan keyin:
- mahsulot upsert → `_bulk` `update` + `doc_as_upsert` — hujjatga faqat mahsulot maydonlari merge bo'ladi, `slot` maydoni tegilmaydi;
- slot o'zgarishi → `_bulk` `update` faqat `{"doc":{"slot":[...]}}` — hujjat qayta yozilmaydi. Yangi egalar bilan birga **siqib chiqarilgan** mahsulotlarning ham slot maydoni yangilanadi;
- soft delete → `_bulk` `delete`.

ES yiqilsa yozuv 500 bermaydi: DB'da commit bo'lgan, xato log'ga tushadi. Production'da bu joyga outbox + retry qo'yilishi kerak — test topshiriq doirasida ongli soddalashtirish. `refresh=wait_for` demo qulayligi uchun yoqilgan (yozuvdan keyingi qidiruv darhol ko'radi), og'ir yozuv oqimida o'chiriladi.

**ES'da `slot` — keyword massiv.** Bitta mahsulot bir nechta slotda turishi mumkin (ikkita javonda bitta tovar), shuning uchun hujjatda slot raqamlari string massiv bo'lib saqlanadi. Qidiruvda `term` to'g'ridan-to'g'ri ishlaydi.

**Hisobot** — bitta SQL: CTE + `COUNT(*) FILTER`, kodda hech qanday loop yo'q. Bitta mahsulot bir nechta slotda tursa, qiymat yig'indisida **bir marta** hisoblanadi ("biriktirilgan mahsulotlar yig'indisi"), slot hisoblagichlari esa slot kesimida qoladi.

**Pul** — `numeric(14,2)` + Go'da `shopspring/decimal`, JSON'da raqam bo'lib chiqadi.

## EXPLAIN (100k mahsulot, bitta kompaniyada 20k slot)

`GET /slots` search'siz — PK bo'yicha index scan, LIMIT bilan darhol to'xtaydi, **0.1ms**:

```
Limit (actual time=0.029..0.087 rows=20)
  -> Nested Loop Left Join
     -> Index Scan using shelf_slot_pkey on shelf_slot s
        Index Cond: (company_id = '...')
     -> Index Scan using product_pkey on product p
```

`GET /slots?search=` ning birinchi varianti bitta so'rovda `OR` bilan edi:

```sql
WHERE s.company_id = $1
  AND (p.name ILIKE '%q%' OR p.sku ILIKE '%q%' OR s.slot_number::text = $2)
```

EXPLAIN ko'rsatdiki, `OR` ikki jadval ustida bo'lgani uchun planner trigram indekslarni ishlata olmaydi — kompaniyaning butun doskasi aylanib chiqiladi (20k slot, 80k buffer hit, **53ms**):

```
Nested Loop Left Join (actual time=17.607..53.248)
  Filter: ((p.name ~~* ...) OR (p.sku ~~* ...) OR ((s.slot_number)::text = ...))
  Rows Removed by Filter: 19998
  Buffers: shared hit=80287
```

Shuning uchun so'rov `UNION` ga ajratildi: nom/sku branch'i mahsulotni trigram indeks bilan topib slotga join qiladi, slot-raqam branch'i PK bilan ishlaydi. Natija — **6.8ms**, indekslar ishlayapti:

```
-> Bitmap Index Scan on product_name_trgm_idx (actual time=3.911 rows=11)
-> Bitmap Index Scan on product_sku_trgm_idx
-> Index Scan using shelf_slot_product_idx on shelf_slot s
Execution Time: 6.862 ms
```

Hisobot so'rovi 20k slotda 25ms (hash join + aggregate) — kompaniya kesimida bir martalik hisobot uchun yetarli; tez-tez chaqiriladigan bo'lsa materialized counter kerak bo'ladi.

## Testlar

Eng xatarli ikki joy qoplangan (`make test`):
- `internal/domain/product_test.go` — bulk dedupe: oxirgi sku g'olib, tartib saqlanadi; slot dedupe (shu jumladan `null` bilan bo'shatish);
- `internal/search/es_test.go` — qidiruv so'rovida `company_id` doim qattiq `filter` ekani (tenant izolyatsiyasi), fuzzy va exact-term tuzilishi.

## Qilinmagan / keyingi qadamlar

- **gRPC qatlam** — vaqt doirasida qo'shilmadi. Servis qatlami (`internal/service`) transportdan ajratilgan, gRPC handler'lar HTTP bilan yonma-yon shu servisning ustiga o'tiradi.
- ES sync uchun outbox pattern (yuqorida aytilgan).
- Docker image: multi-stage, alpine, non-root (`USER 10001`), `tini` PID 1, healthcheck bor.

## Graceful shutdown

SIGINT/SIGTERM'da Fiber yangi so'rovlarni to'xtatib, aktivlarini 10s ichida tugatadi, keyin DB pool yopiladi.
