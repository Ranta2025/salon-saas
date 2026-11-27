import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { describeApiError, friendlyApiMessage, type ApiErrorNotice } from '@/lib/api'
import {
  activeUnitCount,
  addResourceUnits,
  applyUnitActive,
  canAddType,
  canAddUnits,
  canEditEquipment,
  checkUnitCount,
  createResourceType,
  deleteResourceType,
  EQUIPMENT_COUNT_MESSAGE,
  fetchEquipment,
  MAX_UNITS_PER_TYPE,
  futureBookingsNotice,
  nextUnitName,
  renameResourceType,
  RESOURCE_NAME_MAX,
  setResourceUnit,
  validateResourceName,
  type Equipment,
  type ResourceType,
  type ResourceUnit,
} from '../lib/equipment'
import { activeRole } from '@/lib/permissions'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'
import { ManagerOnlyNotice } from '@/app/components/ManagerOnlyNotice'

/** How many services of the catalogue use a type of equipment. */
export function usageLabel(services: number): string {
  if (services <= 0) {
    return 'Ningún servicio lo usa todavía.'
  }
  if (services === 1) {
    return '1 servicio lo usa.'
  }
  return `${services} servicios lo usan.`
}

/**
 * `/panel/equipamiento` (S-F2e-5): types of equipment with their units, the
 * quantity of each, renaming, adding units and the removal by deactivating a
 * unit (the bookings already scheduled keep theirs). Owner/admin only; the page
 * explains the role to everybody else, like the team page.
 */
export function EquipmentPage() {
  const { salonId, salons, loading: meLoading, refresh: refreshMe } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canEditEquipment(role)

  const [equipment, setEquipment] = useState<Equipment>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [unitError, setUnitError] = useState<string | null>(null)
  const [pendingUnitId, setPendingUnitId] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [quantity, setQuantity] = useState('3')
  const [formError, setFormError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchEquipment(salonId)
      .then((list) => {
        if (active) {
          setEquipment(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar el equipamiento del salón.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Equipamiento" title="Equipamiento del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Equipamiento" title="Equipamiento del salón" />
        <ManagerOnlyNotice
          role={role}
          what="ni cambiar el equipamiento del salón"
          loading={meLoading}
          onRetry={refreshMe}
        />
      </>
    )
  }

  const handleCreate = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setFormError(null)
    setNotice(null)
    const checkedName = validateResourceName(name, equipment)
    if (!checkedName.ok) {
      setFormError(checkedName.message)
      return
    }
    const count = Number(quantity)
    const checkedCount = checkUnitCount(count)
    if (!checkedCount.ok) {
      setFormError(checkedCount.message)
      return
    }
    const limit = canAddType(equipment)
    if (!limit.ok) {
      setFormError(limit.message)
      return
    }
    setCreating(true)
    try {
      await createResourceType(salonId, { name: checkedName.name, count })
      setNotice(
        count === 1
          ? `Equipo creado con 1 unidad: «${checkedName.name} 1».`
          : `Equipo creado con ${count} unidades: «${checkedName.name} 1» a «${checkedName.name} ${count}».`,
      )
      setName('')
      setQuantity('3')
      reload()
    } catch (err: unknown) {
      setFormError(friendlyApiMessage(err, 'No pudimos crear el equipo.'))
    } finally {
      setCreating(false)
    }
  }

  /**
   * Activation switch of one unit (S-F2e-5): the row flips before the answer
   * and comes back to its previous state if the server rejects the change. When
   * the unit is deactivated with future bookings, the notice says how many keep
   * it.
   */
  const handleToggleUnit = async (unit: ResourceUnit): Promise<void> => {
    if (pendingUnitId !== null) {
      return
    }
    const next = !unit.active
    setUnitError(null)
    setNotice(null)
    setPendingUnitId(unit.id)
    setEquipment((list) => applyUnitActive(list, unit.id, next))
    try {
      const saved = await setResourceUnit(salonId, unit.id, { active: next })
      const savedActive = saved.active
      if (savedActive !== null) {
        setEquipment((list) => applyUnitActive(list, unit.id, savedActive))
      }
      const message = futureBookingsNotice(saved.futureBookings)
      if (message !== null) {
        setNotice(`${unit.name}: ${message}.`)
      } else {
        setNotice(next ? `${unit.name} vuelve a estar activa.` : `${unit.name} quedó inactiva.`)
      }
    } catch (err: unknown) {
      setEquipment((list) => applyUnitActive(list, unit.id, unit.active))
      setUnitError(describeApiError(err, 'No pudimos cambiar la unidad.').message)
    } finally {
      setPendingUnitId(null)
    }
  }

  const handleRenameUnit = async (unit: ResourceUnit, value: string): Promise<void> => {
    setNotice(null)
    await setResourceUnit(salonId, unit.id, { name: value })
    setNotice('Nombre de la unidad guardado.')
    reload()
  }

  const handleRenameType = async (type: ResourceType, value: string): Promise<void> => {
    setNotice(null)
    await renameResourceType(salonId, type.id, value)
    setNotice('Nombre del equipo guardado.')
    reload()
  }

  const handleAddUnits = async (type: ResourceType, count: number): Promise<void> => {
    setNotice(null)
    await addResourceUnits(salonId, type.id, count)
    setNotice(count === 1 ? 'Se agregó 1 unidad.' : `Se agregaron ${count} unidades.`)
    reload()
  }

  const handleDelete = async (type: ResourceType): Promise<void> => {
    setNotice(null)
    await deleteResourceType(salonId, type.id)
    setNotice(`Se borró el equipo «${type.name}».`)
    reload()
  }

  return (
    <>
      <PageHeader
        eyebrow="Equipamiento"
        title="Equipamiento del salón"
        description="Cada servicio puede usar un tipo de equipo (puestos de peluquería, sillas de barbería, lavacabezas) y cada turno ocupa una unidad además de la profesional. La cantidad de unidades activas es la capacidad real del salón."
      />

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      <form className="panel-card equip-form" onSubmit={handleCreate} noValidate>
        <h2>Nuevo tipo de equipo</h2>
        <p className="page-head__desc">
          Al crearlo se crean también sus unidades con nombre («Pelo 1», «Pelo 2»…) y se pueden
          agregar más después, hasta {MAX_UNITS_PER_TYPE} por equipo.
        </p>
        <div className="equip-form__grid">
          <div className="field">
            <label htmlFor="equip-new-name">Nombre del equipo</label>
            <input
              id="equip-new-name"
              className="input"
              value={name}
              maxLength={RESOURCE_NAME_MAX}
              placeholder="Pelo, Barbería, Lavacabezas…"
              onChange={(event) => setName(event.target.value)}
              required
            />
          </div>
          <div className="field">
            <label htmlFor="equip-new-count">Cantidad de unidades</label>
            <input
              id="equip-new-count"
              className="input"
              type="number"
              min={1}
              max={50}
              step={1}
              value={quantity}
              onChange={(event) => setQuantity(event.target.value)}
              required
            />
          </div>
        </div>
        {formError ? (
          <p className="notice notice--error" role="alert">
            {formError}
          </p>
        ) : null}
        <button type="submit" className="btn btn--wine" disabled={creating}>
          {creating ? 'Creando…' : 'Crear equipo'}
        </button>
      </form>

      {unitError ? (
        <p className="notice notice--error" role="alert">
          {unitError}
        </p>
      ) : null}

      <section className="panel-card" aria-labelledby="equip-list-title">
        <div className="panel-card__head">
          <h2 id="equip-list-title">Tipos y unidades</h2>
          <span className="panel-card__meta">{equipment.length} tipos</span>
        </div>
        {loading && !loaded ? <LoadingState label="Cargando equipamiento…" /> : null}
        {loaded && equipment.length === 0 ? (
          <p className="empty-state">
            El salón todavía no tiene equipamiento. Crea el primer tipo con «Crear equipo».
          </p>
        ) : null}
        <div className="equip-grid">
          {equipment.map((type) => (
            <TypeCard
              key={type.id}
              type={type}
              siblings={equipment.filter((row) => row.id !== type.id)}
              pendingUnitId={pendingUnitId}
              onToggleUnit={(unit) => void handleToggleUnit(unit)}
              onRenameUnit={(unit, value) => handleRenameUnit(unit, value)}
              onRename={(value) => handleRenameType(type, value)}
              onAddUnits={(count) => handleAddUnits(type, count)}
              onDelete={() => handleDelete(type)}
            />
          ))}
        </div>
      </section>
    </>
  )
}

type TypeCardProps = {
  type: ResourceType
  /** Other types of the salon: the duplicate check of the rename field. */
  siblings: readonly ResourceType[]
  pendingUnitId: string | null
  onToggleUnit: (unit: ResourceUnit) => void
  onRenameUnit: (unit: ResourceUnit, value: string) => Promise<void>
  onRename: (value: string) => Promise<void>
  onAddUnits: (count: number) => Promise<void>
  onDelete: () => Promise<void>
}

/** One type of equipment with its units, its size and its own actions. */
function TypeCard({
  type,
  siblings,
  pendingUnitId,
  onToggleUnit,
  onRenameUnit,
  onRename,
  onAddUnits,
  onDelete,
}: TypeCardProps) {
  const [name, setName] = useState(type.name)
  const [renamePending, setRenamePending] = useState(false)
  const [renameError, setRenameError] = useState<string | null>(null)
  const [quantity, setQuantity] = useState('1')
  const [addPending, setAddPending] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  useEffect(() => {
    setName(type.name)
  }, [type.name])

  const addCheck = canAddUnits(type, Number(quantity))
  const usage = usageLabel(type.services)

  const commitName = async (): Promise<void> => {
    if (renamePending) {
      return
    }
    const checked = validateResourceName(name, siblings, type.id)
    if (!checked.ok) {
      setRenameError(checked.message)
      return
    }
    setRenameError(null)
    if (checked.name === type.name) {
      return
    }
    setRenamePending(true)
    try {
      await onRename(checked.name)
    } catch (err: unknown) {
      setRenameError(describeApiError(err, 'No pudimos guardar el nombre.').message)
    } finally {
      setRenamePending(false)
    }
  }

  const commitAdd = async (): Promise<void> => {
    if (addPending) {
      return
    }
    const count = Number(quantity)
    const checked = canAddUnits(type, count)
    if (!checked.ok) {
      setAddError(checked.message)
      return
    }
    setAddError(null)
    setAddPending(true)
    try {
      await onAddUnits(count)
      setQuantity('1')
    } catch (err: unknown) {
      setAddError(describeApiError(err, 'No pudimos agregar las unidades.').message)
    } finally {
      setAddPending(false)
    }
  }

  const commitDelete = async (): Promise<void> => {
    setDeleting(true)
    setDeleteError(null)
    try {
      await onDelete()
      setConfirming(false)
    } catch (err: unknown) {
      setDeleteError(describeApiError(err, 'No pudimos borrar el equipo.').message)
    } finally {
      setDeleting(false)
    }
  }

  const nameId = `equip-name-${type.id}`
  const countId = `equip-count-${type.id}`

  return (
    <article className="panel-card equip-card" aria-labelledby={`equip-${type.id}`}>
      <div className="panel-card__head equip-card__head">
        <h3 id={`equip-${type.id}`}>{type.name}</h3>
        <span className="panel-card__meta">
          {activeUnitCount(type)} de {type.units.length} unidades activas
        </span>
      </div>
      <p className="page-head__desc">{usage}</p>

      <div className="equip-card__fields">
        <div className="field">
          <label htmlFor={nameId}>Nombre del equipo</label>
          <input
            id={nameId}
            className="input"
            value={name}
            maxLength={RESOURCE_NAME_MAX}
            aria-busy={renamePending}
            onChange={(event) => {
              setName(event.target.value)
              setRenameError(null)
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                void commitName()
              }
              if (event.key === 'Escape') {
                setName(type.name)
                setRenameError(null)
              }
            }}
          />
          <p className="field__hint">Hasta {RESOURCE_NAME_MAX} caracteres.</p>
          {renameError ? (
            <p className="field__error" role="alert">
              {renameError}
            </p>
          ) : null}
          <button
            type="button"
            className="btn btn--quiet btn--sm"
            disabled={renamePending}
            onClick={() => void commitName()}
          >
            {renamePending ? 'Guardando…' : 'Guardar nombre'}
          </button>
        </div>

        <div className="field">
          <label htmlFor={countId}>Unidades nuevas</label>
          <div className="equip-card__add">
            <input
              id={countId}
              className="input"
              type="number"
              min={1}
              max={50}
              step={1}
              value={quantity}
              aria-busy={addPending}
              onChange={(event) => {
                setQuantity(event.target.value)
                setAddError(null)
              }}
            />
            <button
              type="button"
              className="btn btn--quiet btn--sm"
              disabled={addPending || !addCheck.ok}
              onClick={() => void commitAdd()}
            >
              {addPending ? 'Agregando…' : 'Agregar unidades'}
            </button>
          </div>
          <p className="field__hint">
            {addCheck.ok
              ? `Se crearán como «${nextUnitName(type)}»…`
              : addCheck.message === EQUIPMENT_COUNT_MESSAGE
                ? EQUIPMENT_COUNT_MESSAGE
                : `${addCheck.message}. Desactivá las que no uses.`}
          </p>
          {addError ? (
            <p className="field__error" role="alert">
              {addError}
            </p>
          ) : null}
        </div>
      </div>

      <h4 className="equip-card__units-title">Unidades</h4>
      {type.units.length === 0 ? (
        <p className="field__hint">Este equipo no tiene unidades: no se ofrecen turnos.</p>
      ) : (
        <ul className="equip-units">
          {type.units.map((unit) => (
            <UnitRow
              key={unit.id}
              unit={unit}
              type={type}
              pending={pendingUnitId === unit.id}
              onToggle={() => onToggleUnit(unit)}
              onRename={(value) => onRenameUnit(unit, value)}
            />
          ))}
        </ul>
      )}

      <div className="equip-card__foot">
        {confirming ? (
          <span className="photo-confirm" role="group" aria-label="Confirmar borrado">
            <span>¿Borrar el equipo? Las unidades con turnos no se pueden borrar.</span>
            <button
              type="button"
              className="btn btn--wine btn--sm"
              disabled={deleting}
              onClick={() => void commitDelete()}
            >
              Sí, borrar
            </button>
            <button
              type="button"
              className="btn btn--quiet btn--sm"
              onClick={() => setConfirming(false)}
            >
              Volver
            </button>
          </span>
        ) : (
          <button
            type="button"
            className="btn btn--quiet btn--sm"
            onClick={() => setConfirming(true)}
            aria-label={`Borrar el equipo ${type.name}`}
          >
            Borrar equipo
          </button>
        )}
        {deleteError ? (
          <p className="notice notice--error" role="alert">
            {deleteError}
          </p>
        ) : null}
      </div>
    </article>
  )
}

type UnitRowProps = {
  unit: ResourceUnit
  type: ResourceType
  pending: boolean
  onToggle: () => void
  onRename: (value: string) => Promise<void>
}

/** One unit: its switch (44 px) and its rename field, revealed on demand. */
function UnitRow({ unit, type, pending, onToggle, onRename }: UnitRowProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(unit.name)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setDraft(unit.name)
  }, [unit.name])

  const commit = async (): Promise<void> => {
    if (saving) {
      return
    }
    const checked = validateResourceName(draft, type.units, unit.id)
    if (!checked.ok) {
      setError(checked.message)
      return
    }
    setError(null)
    if (checked.name === unit.name) {
      setEditing(false)
      return
    }
    setSaving(true)
    try {
      await onRename(checked.name)
      setEditing(false)
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos guardar el nombre de la unidad.').message)
    } finally {
      setSaving(false)
    }
  }

  const inputId = `unit-name-${unit.id}`

  return (
    <li className="equip-unit" data-inactive={!unit.active}>
      <label className="attends equip-unit__switch">
        <input
          type="checkbox"
          role="switch"
          className="attends__input"
          checked={unit.active}
          aria-checked={unit.active}
          aria-busy={pending}
          aria-label={`Equipo activo: ${unit.name}`}
          onChange={onToggle}
        />
        <span className="attends__track" aria-hidden="true">
          <span className="attends__thumb" />
        </span>
        <span className="equip-unit__name">{unit.name}</span>
        <span className={`pill ${unit.active ? 'pill--on' : 'pill--off'}`}>
          {unit.active ? 'Activa' : 'Inactiva'}
        </span>
      </label>
      {editing ? (
        <span className="equip-unit__rename">
          <label className="visually-hidden" htmlFor={inputId}>
            Nombre de la unidad {unit.name}
          </label>
          <input
            id={inputId}
            className="input"
            value={draft}
            maxLength={RESOURCE_NAME_MAX}
            aria-busy={saving}
            onChange={(event) => {
              setDraft(event.target.value)
              setError(null)
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                void commit()
              }
              if (event.key === 'Escape') {
                setDraft(unit.name)
                setError(null)
                setEditing(false)
              }
            }}
          />
          <button type="button" className="btn btn--wine btn--sm" disabled={saving} onClick={() => void commit()}>
            {saving ? 'Guardando…' : 'Guardar'}
          </button>
          <button
            type="button"
            className="btn btn--quiet btn--sm"
            onClick={() => {
              setDraft(unit.name)
              setError(null)
              setEditing(false)
            }}
          >
            Cancelar
          </button>
        </span>
      ) : (
        <button
          type="button"
          className="btn btn--quiet btn--sm"
          aria-label={`Renombrar la unidad ${unit.name}`}
          onClick={() => setEditing(true)}
        >
          Renombrar
        </button>
      )}
      {error ? (
        <p className="field__error" role="alert">
          {error}
        </p>
      ) : null}
    </li>
  )
}
