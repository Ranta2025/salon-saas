import { useState, type FormEvent } from 'react'
import { searchClients, type PickableClient } from '@/features/clients/lib/clientRecord'
import { displayPhone } from '../../lib/phone'

/** Rows painted at once: the rest appears as the person narrows the search. */
const VISIBLE_RESULTS = 8

type ClientPickerProps = {
  /** Client files already fetched by the page: the search never asks again. */
  clients: readonly PickableClient[]
  /** Message of the failed `/clients` request, shown next to the list. */
  error: string | null
  loading: boolean
  /** The file the sale will be linked to, or `null` for a free-text sale. */
  selected: PickableClient | null
  onSelect: (client: PickableClient | null) => void
}

/**
 * Optional client picker of the POS (contract F5, D3c). Choosing a file fills the
 * name of the ticket and sends its `client_id`; choosing nobody leaves the flow
 * exactly as it was. The search filters the list the page already has, so typing
 * never hits the API.
 */
export function ClientPicker({ clients, error, loading, selected, onSelect }: ClientPickerProps) {
  const [term, setTerm] = useState('')
  const matches = searchClients(clients, term)
  const visible = matches.slice(0, VISIBLE_RESULTS)

  const handleSearch = (event: FormEvent<HTMLInputElement>): void => {
    event.preventDefault()
    setTerm(event.currentTarget.value)
  }

  return (
    <div className="field pos-client-picker">
      <label htmlFor="pos-client-picker">Ficha de la clienta (opcional)</label>
      <input
        id="pos-client-picker"
        className="input"
        type="search"
        value={term}
        onChange={handleSearch}
        autoComplete="off"
        placeholder="Buscar por nombre o teléfono…"
        disabled={clients.length === 0}
      />
      {selected ? (
        <p className="field__hint">
          Elegida: <strong>{selected.name}</strong>.{' '}
          <button type="button" className="btn btn--quiet btn--sm" onClick={() => onSelect(null)}>
            Quitar
          </button>
        </p>
      ) : (
        <p className="field__hint">
          Sin ficha elegida la venta se guarda igual, con el nombre escrito a mano.
        </p>
      )}

      {error ? (
        <p className="field__error" role="alert">
          No pudimos cargar las fichas: {error}
        </p>
      ) : loading ? (
        <p className="field__hint">Cargando fichas de clientas…</p>
      ) : clients.length === 0 ? (
        <p className="field__hint">
          Todavía no hay fichas de clientas en el salón: escribí el nombre a mano.
        </p>
      ) : visible.length === 0 ? (
        <p className="field__hint">Ninguna ficha coincide con la búsqueda.</p>
      ) : (
        <ul className="pos-services">
          {visible.map((client) => {
            const isSelected = selected?.id === client.id
            const phone = displayPhone(client.phone ?? '')
            return (
              <li key={client.id}>
                <button
                  type="button"
                  className="pos-service"
                  aria-pressed={isSelected}
                  onClick={() => onSelect(isSelected ? null : client)}
                >
                  <span className="pos-service__name">{client.name}</span>
                  <span className="pos-service__meta">{phone || 'Sin teléfono'}</span>
                  <span className="pos-service__price" aria-hidden="true">
                    {isSelected ? '✓' : 'Elegir'}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {visible.length < matches.length ? (
        <p className="field__hint">
          {matches.length} fichas coinciden: seguí escribiendo para ver el resto.
        </p>
      ) : null}
    </div>
  )
}
