CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE product (
    id           uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   uuid          NOT NULL,
    name         text          NOT NULL,
    sku          text          NOT NULL,
    barcode      text[]        NOT NULL DEFAULT '{}',
    supply_price numeric(14,2) NOT NULL DEFAULT 0,
    retail_price numeric(14,2) NOT NULL DEFAULT 0,
    created_at   timestamptz   NOT NULL DEFAULT now(),
    updated_at   timestamptz   NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

-- Upsert target for POST /products/bulk and tenant-scoped uniqueness.
-- Partial: a soft-deleted row frees its sku for re-creation.
CREATE UNIQUE INDEX product_company_sku_uq
    ON product (company_id, sku)
    WHERE deleted_at IS NULL;

-- GET /slots?search= does ILIKE '%q%' over joined product name/sku;
-- btree cannot serve infix match, trigram GIN can.
CREATE INDEX product_name_trgm_idx ON product USING gin (name gin_trgm_ops);
CREATE INDEX product_sku_trgm_idx  ON product USING gin (sku gin_trgm_ops);

CREATE TABLE shelf_slot (
    company_id  uuid        NOT NULL,
    slot_number int         NOT NULL CHECK (slot_number >= 1),
    product_id  uuid        REFERENCES product (id),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, slot_number)
);

-- DELETE /products frees slots by product_id list.
CREATE INDEX shelf_slot_product_idx
    ON shelf_slot (product_id)
    WHERE product_id IS NOT NULL;
