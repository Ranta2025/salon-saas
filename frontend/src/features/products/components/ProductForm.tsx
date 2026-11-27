import { useEffect, useState, type FormEvent } from 'react'
import {
  MAX_PRODUCT_NAME_LENGTH,
  productPriceInputFromCents,
  validateProductDraft,
  type Product,
  type ProductDraft,
  type ProductField,
  type ProductPayload,
  type ProductUpdatePayload,
} from '../lib/products'

type ProductFormProps = {
  /** The product being replaced, or absent to create a new one. */
  product?: Product
  saving: boolean
  /** Spanish sentence of the server (400/404/409), shown verbatim. */
  serverError: string | null
  onCancel: () => void
  onSubmit: (payload: ProductPayload | ProductUpdatePayload) => void
}

/** The form as it opens: empty for a new product, filled when one is edited. */
function draftOf(product?: Product): ProductDraft {
  if (!product) {
    return { name: '', price: '', stock: '', low_stock_threshold: '', active: true }
  }
  return {
    name: product.name,
    price: productPriceInputFromCents(product.price_cents),
    // The stock is not edited here: the adjustment form owns it (contract F6, D5).
    stock: '',
    low_stock_threshold: String(product.low_stock_threshold),
    active: product.active,
  }
}

/**
 * Form of a product (contract F6): name, price typed in dollars, low-stock threshold
 * and the state. Creating also accepts the initial stock; editing never sends it,
 * because the `PUT` keeps the stock and the adjustments are their own request.
 *
 * The price is parsed to integer cents with the integer parser of the expenses, and
 * the draft is checked with the sentences of the API before sending the request.
 */
export function ProductForm({ product, saving, serverError, onCancel, onSubmit }: ProductFormProps) {
  const editing = product !== undefined
  const [draft, setDraft] = useState<ProductDraft>(() => draftOf(product))
  const [error, setError] = useState<{ field: ProductField; message: string } | null>(null)

  useEffect(() => {
    setDraft(draftOf(product))
    setError(null)
  }, [product])

  const idBase = editing ? `product-edit-${product.id}` : 'product-new'
  const fieldError = (field: ProductField): string | null =>
    error?.field === field ? error.message : null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    const check = validateProductDraft(draft, editing ? 'update' : 'create')
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  return (
    <form className="service-form" onSubmit={handleSubmit} noValidate>
      <div className="service-form__grid">
        <div className="field">
          <label htmlFor={`${idBase}-name`}>Nombre del producto</label>
          <input
            id={`${idBase}-name`}
            className="input"
            type="text"
            autoComplete="off"
            placeholder="Tinte rubio"
            value={draft.name}
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
            aria-invalid={fieldError('name') ? true : undefined}
            aria-describedby={fieldError('name') ? `${idBase}-name-error` : undefined}
            required
          />
          {fieldError('name') ? (
            <p className="field__error" id={`${idBase}-name-error`} role="alert">
              {fieldError('name')}
            </p>
          ) : null}
        </div>

        <div className="field">
          <label htmlFor={`${idBase}-price`}>Precio en dólares</label>
          <input
            id={`${idBase}-price`}
            className="input"
            type="text"
            inputMode="decimal"
            autoComplete="off"
            placeholder="12.50"
            value={draft.price}
            onChange={(event) => setDraft({ ...draft, price: event.target.value })}
            aria-invalid={fieldError('price') ? true : undefined}
            aria-describedby={
              fieldError('price') ? `${idBase}-price-error` : `${idBase}-price-hint`
            }
            required
          />
          {fieldError('price') ? (
            <p className="field__error" id={`${idBase}-price-error`} role="alert">
              {fieldError('price')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-price-hint`}>
              Hasta dos decimales: 12, 12.50 o 12,50. Sin separador de miles.
            </p>
          )}
        </div>

        {editing ? null : (
          <div className="field">
            <label htmlFor={`${idBase}-stock`}>Stock inicial (opcional)</label>
            <input
              id={`${idBase}-stock`}
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="0"
              value={draft.stock}
              onChange={(event) => setDraft({ ...draft, stock: event.target.value })}
              aria-invalid={fieldError('stock') ? true : undefined}
              aria-describedby={
                fieldError('stock') ? `${idBase}-stock-error` : `${idBase}-stock-hint`
              }
            />
            {fieldError('stock') ? (
              <p className="field__error" id={`${idBase}-stock-error`} role="alert">
                {fieldError('stock')}
              </p>
            ) : (
              <p className="field__hint" id={`${idBase}-stock-hint`}>
                Unidades, sin decimales. Sin stock se carga después con un ajuste.
              </p>
            )}
          </div>
        )}

        <div className="field">
          <label htmlFor={`${idBase}-threshold`}>Umbral de stock bajo</label>
          <input
            id={`${idBase}-threshold`}
            className="input"
            type="text"
            inputMode="numeric"
            autoComplete="off"
            placeholder="0"
            value={draft.low_stock_threshold}
            onChange={(event) => setDraft({ ...draft, low_stock_threshold: event.target.value })}
            aria-invalid={fieldError('low_stock_threshold') ? true : undefined}
            aria-describedby={
              fieldError('low_stock_threshold')
                ? `${idBase}-threshold-error`
                : `${idBase}-threshold-hint`
            }
          />
          {fieldError('low_stock_threshold') ? (
            <p className="field__error" id={`${idBase}-threshold-error`} role="alert">
              {fieldError('low_stock_threshold')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-threshold-hint`}>
              Con 0 no hay alerta: el producto se marca cuando el stock queda en el umbral o
              menos.
            </p>
          )}
        </div>

        {editing ? (
          <div className="field">
            <label className="checkbox" htmlFor={`${idBase}-active`}>
              <input
                id={`${idBase}-active`}
                type="checkbox"
                checked={draft.active}
                onChange={(event) => setDraft({ ...draft, active: event.target.checked })}
              />
              <span>Activo: se puede vender en el POS</span>
            </label>
            <p className="field__hint">
              El stock no se edita acá: cambia con un ajuste o con una venta.
            </p>
          </div>
        ) : null}
      </div>

      {serverError ? (
        <p className="notice notice--error" role="alert">
          {serverError}
        </p>
      ) : null}

      <div className="page-head__actions">
        <button type="submit" className="btn btn--wine" disabled={saving}>
          {saving ? 'Guardando…' : editing ? 'Guardar cambios' : 'Crear producto'}
        </button>
        <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={saving}>
          Cancelar
        </button>
      </div>

      <p className="field__hint">
        El nombre admite hasta {MAX_PRODUCT_NAME_LENGTH} caracteres.
      </p>
    </form>
  )
}
