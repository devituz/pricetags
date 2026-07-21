# Test topshiriq (backend, Go)

Kichik servis yozib berishingiz kerak, shartlari pastda. Topshiriq bizdagi real loyihaga o'xshatib tuzilgan, shuning uchun texnologiya bo'yicha talab qattiq: Go 1.21+, PostgreSQL bilan ishlash faqat sqlx orqali (gorm/ent va boshqa ORM kerak emas), Elasticsearch 8, migratsiyalar golang-migrate bilan. Postgres va Elastic docker-compose da ko'tarilsin. HTTP framework o'zingizga qulayini oling (bizda Fiber, lekin bu shart emas).

Mavzu: do'kondagi elektron narx ko'rsatkichlari. Kompaniyada mahsulotlar bor, javonlarda esa raqamlangan slotlar (1 dan N gacha). Slotga mahsulot biriktiriladi, ko'rsatkichda nomi, sku va narxi chiqadi.

Hamma joyda company_id ishtirok etadi — u X-Company-Id headerda keladi, auth yozish kerak emas. Bitta kompaniya boshqasining ma'lumotini hech qayerda ko'rmasligi kerak, shunga alohida e'tibor bering.

Nima qilish kerak:

1) Baza. product jadvali: id (uuid), company_id, name, sku, barcode (bir nechta bo'lishi mumkin), supply_price, retail_price, created_at. O'chirish soft delete bo'lsin (deleted_at). shelf_slot jadvali: slot raqami, company_id, product_id (bo'sh bo'lishi ham mumkin). Slot raqami kompaniya ichida takrorlanmasin. Qolgan detallarni o'zingiz hal qilasiz, faqat README da qanday indeks qo'yganingizni va sababini yozib qo'ying.

2) POST /products/bulk — mahsulotlar ko'p qilib yuboriladi (1000 tagacha), (company_id, sku) bo'yicha upsert: bori yangilanadi, yo'g'i yaratiladi. Muhim joyi: bitta requestda bitta sku ikki marta kelib qolishi mumkin — bunda oxirgisi olinsin va request xato bermasin.

3) PUT /slots — body da [{"slot": 5, "product_id": "..."}] ko'rinishida ro'yxat. Slot band bo'lsa, eskisi olib tashlanib yangisi biriktiriladi.

4) GET /slots?page=&limit=&search= — kompaniya slotlari ro'yxati. Bo'sh slotlar ham chiqishi kerak, foydalanuvchi butun doskani ko'radi. Pagination va umumiy count bo'lsin. Band slotda mahsulot nomi, sku va retail_price ko'rinadi — join bilan oling, slot jadvalida nusxa saqlamang. search nom, sku yoki slot raqami bo'yicha ishlaydi.

5) DELETE /products — id ro'yxati keladi, mahsulotlar soft delete bo'ladi va ularning slotlari bo'shaydi.

6) GET /reports/stock-value — kompaniya bo'yicha: biriktirilgan mahsulotlar supply_price yig'indisi, nechta slot band, nechta bo'sh. Bitta SQL so'rov bilan qiling, kodda loop bilan hisoblamang.

7) Elastic. products indeksi: id, company_id, name, sku, barcode, retail_price, slot (slot string bo'lib saqlanadi). Mahsulot upsert bo'lsa indeksga tushadi. Slot o'zgarsa hujjatda faqat slot maydoni yangilanadi — bulk bilan, butun hujjatni qayta yozmasdan. Soft delete bo'lsa indeksdan o'chadi.

8) GET /products/search?q= — qidiruv Elasticdan: nom bo'yicha (biroz xato yozilsa ham topsin), sku, barcode va slot raqami bo'yicha. Faqat o'z kompaniyasi ichida.

Yana:
- xatolarni normal HTTP kod va JSON bilan qaytaring
- o'zingiz eng murakkab degan joyga 1-2 ta test yozing, hammasini qoplash shart emas
- README: loyiha qanday ko'tariladi, qaysi joyda qanday qaror qabul qilgansiz va nega

Shart emas, lekin plus bo'ladi: gRPC qatlam (bizda gateway -> grpc servis arxitektura), og'ir so'rovlarga EXPLAIN qarab natijasini README ga yozish, graceful shutdown, bitta slotga bir vaqtda ikkita request kelsa nima bo'lishini o'ylab qo'yish.

Tayyor bo'lgach git repo qilib yuboring, commit tarixini o'chirmang. Savol chiqib qolsa bemalol yozavering, savol berish minus emas.
