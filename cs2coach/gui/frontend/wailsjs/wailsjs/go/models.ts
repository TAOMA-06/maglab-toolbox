export namespace gui {
	
	export class GoodRow {
	    round: number;
	    reason: string;
	    highlight: string;
	
	    static createFrom(source: any = {}) {
	        return new GoodRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.reason = source["reason"];
	        this.highlight = source["highlight"];
	    }
	}
	export class SuspectRow {
	    round: number;
	    tags: string;
	    reason: string;
	    cost: number;
	    tick: number;
	
	    static createFrom(source: any = {}) {
	        return new SuspectRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.tags = source["tags"];
	        this.reason = source["reason"];
	        this.cost = source["cost"];
	        this.tick = source["tick"];
	    }
	}
	export class CauseRow {
	    label: string;
	    tag: string;
	    count: number;
	    percent: string;
	    rounds: string;
	
	    static createFrom(source: any = {}) {
	        return new CauseRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.tag = source["tag"];
	        this.count = source["count"];
	        this.percent = source["percent"];
	        this.rounds = source["rounds"];
	    }
	}
	export class AnalyzeSummary {
	    focusWon: number;
	    focusLost: number;
	    causes: CauseRow[];
	    suspects: SuspectRow[];
	    keySwings: string[];
	    goodRounds: GoodRow[];
	
	    static createFrom(source: any = {}) {
	        return new AnalyzeSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.focusWon = source["focusWon"];
	        this.focusLost = source["focusLost"];
	        this.causes = this.convertValues(source["causes"], CauseRow);
	        this.suspects = this.convertValues(source["suspects"], SuspectRow);
	        this.keySwings = source["keySwings"];
	        this.goodRounds = this.convertValues(source["goodRounds"], GoodRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class PlayerItem {
	    name: string;
	    steamID64: string;
	    focus: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PlayerItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.steamID64 = source["steamID64"];
	        this.focus = source["focus"];
	    }
	}
	export class ProbeInfo {
	    path: string;
	    fileName: string;
	    sizeBytes: number;
	    sizeText: string;
	    modTime: string;
	    players: PlayerItem[];
	    isPOV: boolean;
	    povName: string;
	
	    static createFrom(source: any = {}) {
	        return new ProbeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.fileName = source["fileName"];
	        this.sizeBytes = source["sizeBytes"];
	        this.sizeText = source["sizeText"];
	        this.modTime = source["modTime"];
	        this.players = this.convertValues(source["players"], PlayerItem);
	        this.isPOV = source["isPOV"];
	        this.povName = source["povName"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Progress {
	    stage: string;
	    percent: number;
	    message: string;
	    elapsedMs: number;
	    failed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Progress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stage = source["stage"];
	        this.percent = source["percent"];
	        this.message = source["message"];
	        this.elapsedMs = source["elapsedMs"];
	        this.failed = source["failed"];
	    }
	}
	export class RunResult {
	    map: string;
	    totalRounds: number;
	    scoreT: number;
	    scoreCT: number;
	    focusName: string;
	    mode: string;
	    outDir: string;
	    reportHTML: string;
	    reportMD: string;
	    matchJSON: string;
	    analysisJSON: string;
	    promptFile: string;
	    html: string;
	    markdown: string;
	    summary: AnalyzeSummary;
	    warning: string;
	    interpreted: boolean;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new RunResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.map = source["map"];
	        this.totalRounds = source["totalRounds"];
	        this.scoreT = source["scoreT"];
	        this.scoreCT = source["scoreCT"];
	        this.focusName = source["focusName"];
	        this.mode = source["mode"];
	        this.outDir = source["outDir"];
	        this.reportHTML = source["reportHTML"];
	        this.reportMD = source["reportMD"];
	        this.matchJSON = source["matchJSON"];
	        this.analysisJSON = source["analysisJSON"];
	        this.promptFile = source["promptFile"];
	        this.html = source["html"];
	        this.markdown = source["markdown"];
	        this.summary = this.convertValues(source["summary"], AnalyzeSummary);
	        this.warning = source["warning"];
	        this.interpreted = source["interpreted"];
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Settings {
	    mode: string;
	    provider: string;
	    apiKey: string;
	    baseURL: string;
	    model: string;
	    ollamaURL: string;
	    ollamaModel: string;
	    outDir: string;
	    useLLM: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.provider = source["provider"];
	        this.apiKey = source["apiKey"];
	        this.baseURL = source["baseURL"];
	        this.model = source["model"];
	        this.ollamaURL = source["ollamaURL"];
	        this.ollamaModel = source["ollamaModel"];
	        this.outDir = source["outDir"];
	        this.useLLM = source["useLLM"];
	    }
	}

}

