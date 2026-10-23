import {i18n} from "./i18n";

export type CredentialTab="env"|"ssh";

/** Words a failed credential call for the person. An unknown server text is shown as it came, never swallowed. */
export function credentialMessage(cause:unknown,fallback:string,tab:CredentialTab):string{
 if(!(cause instanceof Error))return fallback;
 const text=cause.message;
 if(/already exists/i.test(text))return i18n.t(tab==="env"?"computer:credentials.error.env_exists":"computer:credentials.error.exists");
 if(/busy|current task|active task/i.test(text))return i18n.t("computer:credentials.error.busy");
 if(/private key.*parsed|valid private key/i.test(text))return i18n.t("computer:credentials.error.private_key_invalid");
 if(/private key filename/i.test(text))return i18n.t("computer:credentials.error.private_key_filename");
 if(/SSH directory/i.test(text))return i18n.t("computer:credentials.error.ssh_dir");
 if(/OpenSSH tools/i.test(text))return i18n.t("computer:credentials.error.openssh_missing");
 if(/SSH key response|read SSH keys|SSH key operation/i.test(text))return i18n.t("computer:credentials.error.key_io");
 return text||fallback;
}
