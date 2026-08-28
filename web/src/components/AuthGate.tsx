import '../styles/auth.css';
import type { JSX } from 'preact';
import { useEffect, useId, useRef, useState } from 'preact/hooks';
import type { TokenScope } from '../api/auth';
import { tokenStore } from '../api/auth';

export interface AuthGateProps {
  /** Set by the app when any operator request returns 401. */
  needed: boolean;
  onSubmit: (token: string, scope: TokenScope) => void;
  onDismiss?: () => void;
  /** Present when a supplied token was rejected. */
  rejected?: boolean;
}

/**
 * The token prompt for controllers running in bearer mode. Renders nothing
 * otherwise, so auth-disabled deployments never see it.
 *
 * Intentionally not built on Modal: this gate is not a view over some record
 * the operator opened, it is the whole surface until a token exists, and it
 * must not depend on anything that itself needs an authorised request.
 */
export function AuthGate(props: AuthGateProps): JSX.Element | null {
  const [token, setToken] = useState('');
  const [scope, setScope] = useState<TokenScope>('session');
  const field = useRef<HTMLInputElement>(null);
  const titleID = useId();
  const scopeName = `${titleID}-scope`;
  const { needed, onDismiss } = props;

  useEffect(() => {
    if (!needed) return undefined;
    field.current?.focus();
    if (!onDismiss) return undefined;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.stopPropagation();
      onDismiss();
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [needed, onDismiss]);

  if (!needed) return null;

  const ready = token.trim() !== '';

  return (
    <div class="auth-scrim">
      <div class="auth-gate" role="dialog" aria-modal="true" aria-labelledby={titleID}>
        <h2 class="auth-title" id={titleID}>
          Operator token required
        </h2>
        <p class="auth-note">
          This controller answers the operator API only with a bearer token. It is held in this
          browser and sent to this controller alone.
        </p>

        {props.rejected ? (
          <p class="auth-reject" role="alert">
            The controller rejected that token.
          </p>
        ) : null}

        <form
          class="auth-form"
          onSubmit={(event) => {
            event.preventDefault();
            const value = token.trim();
            if (value === '') return;
            setToken('');
            props.onSubmit(value, scope);
          }}
        >
          <label class="auth-field">
            <span class="label">token</span>
            <input
              ref={field}
              type="password"
              autocomplete="off"
              spellcheck={false}
              value={token}
              aria-invalid={props.rejected ? 'true' : 'false'}
              onInput={(event) => setToken(event.currentTarget.value)}
            />
          </label>

          <fieldset class="auth-scope">
            <legend class="label">keep it</legend>
            <label>
              <input
                type="radio"
                name={scopeName}
                checked={scope === 'session'}
                onChange={() => setScope('session')}
              />
              <span>this tab only</span>
            </label>
            <label>
              <input
                type="radio"
                name={scopeName}
                checked={scope === 'local'}
                onChange={() => setScope('local')}
              />
              <span>remember on this device</span>
            </label>
          </fieldset>

          <div class="auth-actions">
            {onDismiss ? (
              <button type="button" class="link" onClick={onDismiss}>
                not now
              </button>
            ) : null}
            <button type="submit" disabled={!ready}>
              unlock
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// Enough tail to tell two tokens apart, never enough to be one. Short values
// are masked whole: four of six characters is the token.
const TAIL = 4;
const MASKABLE = 12;

/**
 * Top-bar indicator for a stored token, with the sign-out action. Renders
 * nothing when no token is stored, which is every auth-disabled deployment.
 */
export function AuthStatus(props: { onChange: () => void }): JSX.Element | null {
  const [, revision] = useState(0);
  useEffect(() => tokenStore.subscribe(() => revision((value) => value + 1)), []);

  const token = tokenStore.get();
  if (!token) return null;

  const scope = tokenStore.scope();

  return (
    <div class="auth-status">
      <span class="label">token</span>
      <span
        class="auth-mask"
        title={scope === 'local' ? 'remembered on this device' : 'held for this tab only'}
      >
        {token.length >= MASKABLE ? `…${token.slice(-TAIL)}` : '…'}
      </span>
      <button
        type="button"
        class="link"
        onClick={() => {
          tokenStore.clear();
          props.onChange();
        }}
      >
        sign out
      </button>
    </div>
  );
}
