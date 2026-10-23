/**
 * Environment-variable name rule for Settings > Keys & secrets.
 * Mirror of `allowedSecretEnv` / `secretEnvName` in internal/app/secrets.go. Change both together;
 * `test:secret-env-name` pins the UI side and internal/app/secrets_test.go pins the Go side with the same cases.
 */
export const SECRET_ENV_PATTERN=/^[A-Z_][A-Z0-9_]{0,127}$/;
export const SECRET_ENV_MAX_LENGTH=128;
export const SECRET_ENV_RESERVED_PREFIX="LD_";
export const SECRET_ENV_RESERVED_NAMES=["PATH","HOME","BASH_ENV","ENV","DISPLAY","TOFI_WORKSPACE","PWD","TMPDIR"] as const;

/** Typing aid: lowercase becomes uppercase, spaces and hyphens become underscores. Anything else is left for the error to explain. */
export function normalizeSecretEnvInput(raw:string):string{return raw.toUpperCase().replace(/[\s-]/g,"_")}

export type SecretEnvProblem=
 |{code:"bad_chars";chars:string}
 |{code:"leading_digit"}
 |{code:"too_long"}
 |{code:"reserved";name:string};

export function isReservedSecretEnv(name:string):boolean{return name.startsWith(SECRET_ENV_RESERVED_PREFIX)||(SECRET_ENV_RESERVED_NAMES as readonly string[]).includes(name)}

/** null means the backend accepts the name (empty input is not a problem to report; the form handles "required"). */
export function secretEnvProblem(name:string):SecretEnvProblem|null{
 if(!name)return null;
 const bad=[...new Set(name.replace(/[A-Z0-9_]/g,""))];
 if(bad.length)return {code:"bad_chars",chars:bad.join(" ")};
 if(/^[0-9]/.test(name))return {code:"leading_digit"};
 if(name.length>SECRET_ENV_MAX_LENGTH)return {code:"too_long"};
 if(isReservedSecretEnv(name))return {code:"reserved",name};
 return null;
}

/** Same verdict as Go `allowedSecretEnv`. */
export function allowedSecretEnv(name:string):boolean{return SECRET_ENV_PATTERN.test(name)&&!isReservedSecretEnv(name)}
