import type { Bot } from "./types";
import { useDebugMode } from "./debugMode";
import { MCPSettings } from "./MCPSettings";
import { SkillLibrary } from "./settings/SkillLibrary";

export function ExtensionPanel(props:{bots:Bot[];kind:"mcp"|"skills";refreshToken?:number}) {
 return props.kind === "mcp" ? <MCPSettings bots={props.bots} refreshToken={props.refreshToken}/> : <SkillsPage refreshToken={props.refreshToken}/>;
}
function SkillsPage({refreshToken}:{refreshToken?:number}) {
 const debug=useDebugMode();
 return <SkillLibrary refreshToken={refreshToken} debug={debug}/>;
}
