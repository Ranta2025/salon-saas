import { SecurityCopyButton } from './SecurityCopyButton'

type SecurityRecoveryCodesProps = {
  codes: readonly string[]
  /** Only this explicit confirmation hides the codes: they cannot be shown again. */
  onAcknowledge: () => void
}

/**
 * The 8 recovery codes (contract F10, D6/D12). They are shown ONCE: the server keeps
 * only their hashes. The warning, the copy button and the explicit «Ya los guardé»
 * confirmation are the whole contract of this card; nothing else hides the list.
 */
export function SecurityRecoveryCodes({ codes, onAcknowledge }: SecurityRecoveryCodesProps) {
  return (
    <section className="panel-card" aria-labelledby="security-codes-title">
      <div className="panel-card__head">
        <h2 id="security-codes-title">Tus códigos de recuperación</h2>
        <span className="pill pill--on">Verificación activada</span>
      </div>
      <p className="notice notice--warning" role="alert">
        Guardá estos códigos ahora, en un lugar seguro. Son la única forma de entrar si perdés tu
        celular, y no se vuelven a mostrar. Cada código sirve una sola vez. No salgas de esta pantalla hasta confirmar que los guardaste.
      </p>
      <ul className="security-codes" aria-label="Códigos de recuperación">
        {codes.map((code) => (
          <li key={code}>
            <code>{code}</code>
          </li>
        ))}
      </ul>
      <div className="security-actions">
        <SecurityCopyButton text={codes.join('\n')} label="Copiar los códigos" />
        <button type="button" className="btn btn--wine" onClick={onAcknowledge}>
          Ya los guardé
        </button>
      </div>
    </section>
  )
}
