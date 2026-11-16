#!/usr/bin/env python3
"""Edit an existing Word file and keep its formatting.

Subcommands: read, replace, comment, accept, reject, check, unpack, pack.
Every subcommand has --help. The input file is never changed: each editing
command writes a new .docx (-o) and refuses an output that is the input. A
command that cannot do its job exits non-zero and writes nothing; it never
reports success on a failure. Needs lxml (python3-lxml on the Bot computer).

read     print the structure: paragraphs with styles, tables, headers and footers,
         comments with their anchored text, tracked changes shown as {+added+} and [-deleted-].
replace  change text. --tracked records it as a tracked change (only the words that
         differ are marked); without it the text is replaced in place. --map fills many
         placeholders from a JSON file.
comment  add a comment anchored to a piece of text.
accept   accept every tracked change; reject undoes them.
check    test that the package is sound; with --original, that every text change is tracked.
unpack   extract the package to a folder (for hand editing of the XML); pack zips it back.

Text is found across run boundaries, so a phrase that Word split into several
runs is still found. A match must be unique unless --all or --nth is given.
"""
from __future__ import annotations

import argparse
import copy
import datetime
import json
import os
import re
import stat
import sys
import tempfile
import zipfile

try:
    from lxml import etree
except ImportError:
    etree = None

W = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
XML_NS = "http://www.w3.org/XML/1998/namespace"
CT_NS = "http://schemas.openxmlformats.org/package/2006/content-types"
REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"
COMMENTS_REL = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments"
COMMENTS_CT = "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"
OBJ = "￼"
MAX_PACKAGE_BYTES = 1 << 30
TEXT_PARTS = re.compile(r"^word/(document|footnotes|endnotes|header\d*|footer\d*)\.xml$")
PROP_CHANGES = ("rPrChange", "pPrChange", "sectPrChange", "tblPrChange", "tblGridChange", "trPrChange", "tcPrChange", "tblPrExChange", "numberingChange")
UNSUPPORTED = ("cellIns", "cellDel", "cellMerge")


def q(tag):
    return "{%s}%s" % (W, tag)


def fail(message, code=1):
    print("error: %s" % message, file=sys.stderr)
    sys.exit(code)


# ------------------------------------------------------------------ package

class Package:
    def __init__(self, path):
        if not os.path.isfile(path):
            fail("%s is not a file" % path)
        self.path = path
        self.order, self.data, self.trees, self.dirty = [], {}, {}, set()
        try:
            with zipfile.ZipFile(path) as archive:
                total = 0
                for info in archive.infolist():
                    name = info.filename
                    if stat.S_ISLNK(info.external_attr >> 16):
                        fail("%s holds a symbolic link entry %r" % (path, name))
                    if name.startswith("/") or ".." in name.split("/") or "\\" in name or re.match(r"^[A-Za-z]:", name):
                        fail("%s holds an unsafe entry name %r" % (path, name))
                    if info.is_dir():
                        continue
                    total += info.file_size
                    if total > MAX_PACKAGE_BYTES:
                        fail("%s unpacks to more than 1 GiB" % path)
                    if name in self.data:
                        fail("%s holds the entry %r twice" % (path, name))
                    self.order.append(name)
                    self.data[name] = archive.read(info)
        except zipfile.BadZipFile as exc:
            fail("%s is not a valid Word file: %s" % (path, exc))
        if "word/document.xml" not in self.data:
            fail("%s has no word/document.xml; it is not a .docx file" % path)

    def tree(self, name):
        if name not in self.trees:
            parser = etree.XMLParser(resolve_entities=False, no_network=True, load_dtd=False, huge_tree=False)
            try:
                self.trees[name] = etree.fromstring(self.data[name], parser)
            except etree.XMLSyntaxError as exc:
                fail("%s in %s is not well-formed XML: %s" % (name, self.path, exc))
        return self.trees[name]

    def mark(self, name):
        self.dirty.add(name)

    def text_parts(self):
        return [n for n in self.order if TEXT_PARTS.match(n)]

    def add_part(self, name, root):
        self.order.append(name)
        self.data[name] = b""
        self.trees[name] = root
        self.dirty.add(name)

    def save(self, output):
        if os.path.exists(output) and os.path.samefile(self.path, output):
            fail("the output %s is the input file; write to a new file" % output)
        parent = os.path.dirname(os.path.abspath(output))
        os.makedirs(parent, exist_ok=True)
        names = [n for n in self.order if n != "[Content_Types].xml"]
        if "[Content_Types].xml" in self.order:
            names.insert(0, "[Content_Types].xml")
        handle, temp = tempfile.mkstemp(prefix=".docx-edit-", suffix=".tmp", dir=parent)
        os.close(handle)
        try:
            with zipfile.ZipFile(temp, "w", zipfile.ZIP_DEFLATED) as archive:
                for name in names:
                    if name in self.dirty:
                        payload = etree.tostring(self.trees[name], xml_declaration=True, encoding="UTF-8", standalone=True)
                    else:
                        payload = self.data[name]
                    archive.writestr(name, payload)
            os.replace(temp, output)
        finally:
            if os.path.exists(temp):
                os.remove(temp)
        print("wrote %s" % output)


def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def next_id(pkg):
    top = 0
    for name in pkg.text_parts():
        for element in pkg.tree(name).iter():
            value = element.get(q("id"))
            if value is not None and value.isdigit():
                top = max(top, int(value))
    return top + 1


# ------------------------------------------------------------------ paragraphs and runs

def content_children(run):
    return [c for c in run if c.tag not in (q("rPr"), q("lastRenderedPageBreak"))]


def run_text(run):
    """The text of a run that holds one kind of content; an object placeholder for anything that is not text."""
    kids = content_children(run)
    if not kids:
        return ""
    if all(c.tag == q("t") for c in kids):
        return "".join(c.text or "" for c in kids)
    if len(kids) == 1:
        if kids[0].tag == q("tab"):
            return "\t"
        if kids[0].tag in (q("br"), q("cr")):
            return "\n"
    return OBJ


def live_runs(p):
    """Runs that show text, in document order, with whether each sits inside a tracked insertion."""
    out = []
    for child in p:
        if child.tag == q("r"):
            out.append((child, False))
        elif child.tag in (q("ins"), q("moveTo")):
            out.extend((r, True) for r in child if r.tag == q("r"))
        elif child.tag in (q("hyperlink"), q("smartTag"), q("fldSimple")):
            out.extend((r, False) for r in child if r.tag == q("r"))
        elif child.tag == q("sdt"):
            content = child.find(q("sdtContent"))
            if content is not None:
                out.extend((r, False) for r in content if r.tag == q("r"))
    return out


def flat_text(p):
    out = []
    for run, _ in live_runs(p):
        kids = content_children(run)
        for kid in kids:
            if kid.tag == q("t"):
                out.append(kid.text or "")
            elif kid.tag == q("tab"):
                out.append("\t")
            elif kid.tag in (q("br"), q("cr")):
                out.append("\n")
            else:
                out.append(OBJ)
    return "".join(out)


def atomize(p):
    """Split runs that hold several kinds of content so each run holds one."""
    for run, _ in live_runs(p):
        kids = content_children(run)
        if len(kids) < 2 or all(k.tag == q("t") for k in kids):
            if kids and all(k.tag == q("t") for k in kids) and len(kids) > 1:
                joined = "".join(k.text or "" for k in kids)
                for extra in kids[1:]:
                    run.remove(extra)
                set_text(kids[0], joined)
            continue
        groups, current = [], []
        for kid in kids:
            if kid.tag == q("t") and current and current[-1].tag == q("t"):
                current.append(kid)
            else:
                if current:
                    groups.append(current)
                current = [kid]
        groups.append(current)
        anchor = run
        for group in groups[1:]:
            fresh = etree.Element(run.tag, attrib=dict(run.attrib), nsmap=run.nsmap)
            rpr = run.find(q("rPr"))
            if rpr is not None:
                fresh.append(copy.deepcopy(rpr))
            for kid in group:
                fresh.append(kid)
            anchor.addnext(fresh)
            anchor = fresh
        for group in groups[:1]:
            if len(group) > 1:
                joined = "".join(k.text or "" for k in group)
                for extra in group[1:]:
                    run.remove(extra)
                set_text(group[0], joined)
        for lost in run.findall(q("lastRenderedPageBreak")):
            run.remove(lost)


def set_text(t, text):
    t.text = text
    t.set("{%s}space" % XML_NS, "preserve")


def index(p):
    text, spans, pos = [], [], 0
    for run, inside_ins in live_runs(p):
        seg = run_text(run)
        if seg:
            spans.append((run, pos, pos + len(seg), inside_ins))
            text.append(seg)
            pos += len(seg)
    return "".join(text), spans


def split_run(run, offset):
    t = run.find(q("t"))
    text = t.text or ""
    set_text(t, text[:offset])
    fresh = etree.Element(run.tag, attrib=dict(run.attrib), nsmap=run.nsmap)
    rpr = run.find(q("rPr"))
    if rpr is not None:
        fresh.append(copy.deepcopy(rpr))
    piece = etree.SubElement(fresh, q("t"))
    set_text(piece, text[offset:])
    run.addnext(fresh)


def split_at(p, position):
    _, spans = index(p)
    for run, start, end, _ in spans:
        if start < position < end:
            split_run(run, position - start)
            return


def new_run(template_run, text):
    run = etree.Element(q("r"))
    if template_run is not None:
        rpr = template_run.find(q("rPr"))
        if rpr is not None:
            run.append(copy.deepcopy(rpr))
    t = etree.SubElement(run, q("t"))
    set_text(t, text)
    return run


TOKEN = re.compile(r"[　-鿿豈-﫿]|[A-Za-z0-9_]+|\s+|[^\w\s]")


def trim_common(old, new):
    """Keep only the middle that differs, cut at word boundaries."""
    a, b = TOKEN.findall(old), TOKEN.findall(new)
    head = 0
    while head < len(a) and head < len(b) and a[head] == b[head]:
        head += 1
    tail = 0
    while tail < len(a) - head and tail < len(b) - head and a[len(a) - 1 - tail] == b[len(b) - 1 - tail]:
        tail += 1
    prefix = "".join(a[:head])
    return len(prefix), "".join(a[head:len(a) - tail]), "".join(b[head:len(b) - tail])


def make_marker(tag, ident, author):
    element = etree.Element(q(tag))
    element.set(q("id"), str(ident))
    element.set(q("author"), author)
    element.set(q("date"), now())
    return element


class Ids:
    def __init__(self, start):
        self.value = start

    def take(self):
        value = self.value
        self.value += 1
        return value


def apply_edit(p, start, end, new, tracked, author, ids):
    """Replace the text of paragraph p between offsets start and end by new."""
    split_at(p, start)
    split_at(p, end)
    _, spans = index(p)
    affected = [s for s in spans if s[1] >= start and s[2] <= end]
    if tracked and any(s[3] for s in affected):
        fail("the text lies inside an existing tracked insertion; accept or reject the tracked changes first (accept or reject subcommand)")
    if affected:
        first = affected[0][0]
        if not tracked:
            if new:
                first.addprevious(new_run(first, new))
            for run, _, _, _ in affected:
                run.getparent().remove(run)
            return
        last_wrapper = None
        for run, _, _, _ in affected:
            wrapper = make_marker("del", ids.take(), author)
            run.addprevious(wrapper)
            wrapper.append(run)
            for t in run.findall(q("t")):
                t.tag = q("delText")
            for t in run.findall(q("instrText")):
                t.tag = q("delInstrText")
            last_wrapper = wrapper
        if new:
            ins = make_marker("ins", ids.take(), author)
            ins.append(new_run(first, new))
            last_wrapper.addnext(ins)
        return
    # pure insertion at offset start
    if not new:
        return
    before = [s for s in spans if s[2] == start]
    after = [s for s in spans if s[1] == start]
    if before:
        run, inside = before[-1][0], before[-1][3]
        if tracked and inside:
            fail("the insertion point lies inside an existing tracked insertion; accept or reject the tracked changes first")
        fresh = new_run(run, new)
        if tracked:
            ins = make_marker("ins", ids.take(), author)
            ins.append(fresh)
            fresh = ins
        run.addnext(fresh)
    elif after:
        run, inside = after[0][0], after[0][3]
        if tracked and inside:
            fail("the insertion point lies inside an existing tracked insertion; accept or reject the tracked changes first")
        fresh = new_run(run, new)
        if tracked:
            ins = make_marker("ins", ids.take(), author)
            ins.append(fresh)
            fresh = ins
        run.addprevious(fresh)
    else:
        fail("no place found to insert the text")


def all_paragraphs(pkg, names):
    for name in names:
        for p in pkg.tree(name).iter(q("p")):
            yield name, p


def locate(pkg, names, find, case=True):
    """All occurrences of find as (part, paragraph, start, end), in document order."""
    found = []
    needle = find if case else find.lower()
    for name, p in all_paragraphs(pkg, names):
        flat = flat_text(p)
        hay = flat if case else flat.lower()
        if needle not in hay:
            continue
        atomize(p)
        text, _ = index(p)
        hay = text if case else text.lower()
        position = 0
        while True:
            at = hay.find(needle, position)
            if at < 0:
                break
            found.append((name, p, at, at + len(needle)))
            position = at + len(needle)
    return found


def context(p, start, end, width=30):
    text, _ = index(p)
    return "%s[%s]%s" % (text[max(0, start - width):start], text[start:end], text[end:end + width])


def choose(found, find, args):
    if not found:
        return []
    if args.nth:
        if args.nth < 1 or args.nth > len(found):
            fail("--nth %d is outside the %d matches of %r" % (args.nth, len(found), find))
        return [found[args.nth - 1]]
    if getattr(args, "all", False):
        return found
    if len(found) > 1:
        lines = ["%d matches of %r; pass --nth N for one or --all for every match:" % (len(found), find)]
        for number, (name, p, s, e) in enumerate(found, start=1):
            lines.append("  %d. %s: ...%s..." % (number, name, context(p, s, e)))
        fail("\n".join(lines))
    return found


def explain_missing(pkg, names, find):
    hints = []
    squeezed = re.sub(r"\s+", " ", find).strip()
    if squeezed != find and locate(pkg, names, squeezed):
        hints.append("the text exists with single spaces: %r" % squeezed)
    if locate(pkg, names, find, case=False):
        hints.append("the text exists with different capitals; matching is case sensitive")
    plain = find.replace("’", "'").replace("“", '"').replace("”", '"')
    if plain != find and locate(pkg, names, plain):
        hints.append("the document uses plain quotes where the text has curly ones")
    curly = find.replace("'", "’")
    if curly != find and locate(pkg, names, curly):
        hints.append("the document uses curly quotes")
    return " Hint: " + "; ".join(hints) if hints else " Use the read subcommand to see the exact text."


# ------------------------------------------------------------------ replace

def run_replace(pkg, pairs, tracked, author, whole, args):
    names = pkg.text_parts() if not args.body_only else ["word/document.xml"]
    ids = Ids(next_id(pkg))
    total = 0
    for find, replace in pairs:
        if not find:
            fail("an empty text to find")
        if OBJ in find:
            fail("the text to find cannot contain the object placeholder character")
        if "\n" in replace or "\t" in replace:
            fail("tabs and line breaks in the new text are not supported; change paragraphs one at a time or edit the XML after unpack")
        if find == replace:
            fail("the new text equals the text to find: %r" % find)
        found = choose(locate(pkg, names, find), find, args)
        if not found:
            fail("the text %r is not in the document.%s" % (find, explain_missing(pkg, names, find)))
        for name, p, start, end in reversed(found):
            if tracked and not whole:
                offset, old, new = trim_common(find, replace)
                start2 = start + offset
                apply_edit(p, start2, start2 + len(old), new, True, author, ids)
            else:
                apply_edit(p, start, end, replace, tracked, author, ids)
            pkg.mark(name)
            total += 1
    return total


def cmd_replace(args):
    pkg = Package(args.docx)
    if args.map:
        with open(args.map, encoding="utf-8") as handle:
            data = json.load(handle)
        if isinstance(data, dict):
            pairs = list(data.items())
        else:
            pairs = [(item["find"], item["replace"]) for item in data]
    elif args.find is not None and args.replace is not None:
        pairs = [(args.find, args.replace)]
    else:
        fail("give --find and --replace, or --map FILE")
    total = run_replace(pkg, pairs, args.tracked, args.author, args.whole, args)
    pkg.save(args.output)
    print("%d replacement(s) made%s" % (total, " as tracked changes by %s" % args.author if args.tracked else " without tracking"))


# ------------------------------------------------------------------ comments

def cmd_comment(args):
    pkg = Package(args.docx)
    found = choose(locate(pkg, ["word/document.xml"], args.find), args.find, args)
    if not found:
        fail("the text %r is not in the body of the document.%s" % (args.find, explain_missing(pkg, ["word/document.xml"], args.find)))
    ids = Ids(next_id(pkg))
    comments_name = "word/comments.xml"
    if comments_name not in pkg.data:
        root = etree.Element(q("comments"), nsmap={"w": W})
        pkg.add_part(comments_name, root)
        content = pkg.tree("[Content_Types].xml")
        override = etree.SubElement(content, "{%s}Override" % CT_NS)
        override.set("PartName", "/word/comments.xml")
        override.set("ContentType", COMMENTS_CT)
        pkg.mark("[Content_Types].xml")
        rels = pkg.tree("word/_rels/document.xml.rels")
        used = [int(m.group(1)) for r in rels for m in [re.match(r"rId(\d+)$", r.get("Id", ""))] if m]
        rel = etree.SubElement(rels, "{%s}Relationship" % REL_NS)
        rel.set("Id", "rId%d" % (max(used or [0]) + 1))
        rel.set("Type", COMMENTS_REL)
        rel.set("Target", "comments.xml")
        pkg.mark("word/_rels/document.xml.rels")
    comments = pkg.tree(comments_name)
    for name, p, start, end in reversed(found):
        split_at(p, start)
        split_at(p, end)
        _, spans = index(p)
        inside = [s for s in spans if s[1] >= start and s[2] <= end]
        if not inside:
            fail("nothing to anchor the comment to")
        ident = ids.take()
        first, last = inside[0][0], inside[-1][0]
        starter = etree.Element(q("commentRangeStart"))
        starter.set(q("id"), str(ident))
        ender = etree.Element(q("commentRangeEnd"))
        ender.set(q("id"), str(ident))
        ref = etree.Element(q("r"))
        etree.SubElement(ref, q("commentReference")).set(q("id"), str(ident))
        first.addprevious(starter)
        last.addnext(ender)
        ender.addnext(ref)
        comment = etree.SubElement(comments, q("comment"))
        comment.set(q("id"), str(ident))
        comment.set(q("author"), args.author)
        comment.set(q("date"), now())
        comment.set(q("initials"), args.initials or "".join(w[0] for w in args.author.split())[:3].upper() or "A")
        for line in args.text.split("\n"):
            para = etree.SubElement(comment, q("p"))
            etree.SubElement(etree.SubElement(para, q("r")), q("annotationRef"))
            run = etree.SubElement(para, q("r"))
            set_text(etree.SubElement(run, q("t")), line)
        pkg.mark(name)
    pkg.mark(comments_name)
    pkg.save(args.output)
    print("%d comment(s) added by %s" % (len(found), args.author))


# ------------------------------------------------------------------ accept and reject

def unwrap(element):
    parent = element.getparent()
    index_at = parent.index(element)
    for offset, child in enumerate(list(element)):
        parent.insert(index_at + offset, child)
    parent.remove(element)


def in_properties(element):
    return element.getparent() is not None and element.getparent().tag == q("rPr")


def resolve(root, accept):
    counts = {"insertions": 0, "deletions": 0, "property changes": 0, "paragraph joins": 0, "table rows": 0}
    for tag in UNSUPPORTED:
        if next(root.iter(q(tag)), None) is not None:
            fail("the file has tracked table-cell changes (%s); resolve them in Word" % tag)
    for tag in PROP_CHANGES:
        for element in list(root.iter(q(tag))):
            counts["property changes"] += 1
            parent = element.getparent()
            if not accept:
                if tag == "rPrChange":
                    old = element.find(q("rPr"))
                    for child in list(parent):
                        parent.remove(child)
                    if old is not None:
                        for child in old:
                            parent.append(copy.deepcopy(child))
                    continue
                if tag == "pPrChange":
                    old = element.find(q("pPr"))
                    keep = [c for c in parent if c.tag in (q("rPr"), q("sectPr"))]
                    for child in list(parent):
                        parent.remove(child)
                    if old is not None:
                        for child in old:
                            parent.append(copy.deepcopy(child))
                    for child in keep:
                        parent.append(child)
                    continue
                fail("cannot reject a tracked %s change; accept it or edit in Word" % tag)
            parent.remove(element)
    for row in list(root.iter(q("tr"))):
        properties = row.find(q("trPr"))
        if properties is None:
            continue
        for marker in list(properties):
            if marker.tag == q("ins"):
                if accept:
                    properties.remove(marker)
                else:
                    row.getparent().remove(row)
                    counts["table rows"] += 1
                    break
            elif marker.tag == q("del"):
                if accept:
                    row.getparent().remove(row)
                    counts["table rows"] += 1
                    break
                properties.remove(marker)
    merges = []
    for p in root.iter(q("p")):
        mark = p.find("%s/%s" % (q("pPr"), q("rPr")))
        if mark is None:
            continue
        for marker in list(mark):
            if marker.tag in (q("ins"), q("moveTo")):
                if accept:
                    mark.remove(marker)
                else:
                    merges.append(p)
            elif marker.tag in (q("del"), q("moveFrom")):
                if accept:
                    merges.append(p)
                else:
                    mark.remove(marker)
    keep_tags = (q("ins"), q("moveTo")) if accept else (q("del"), q("moveFrom"))
    drop_tags = (q("del"), q("moveFrom")) if accept else (q("ins"), q("moveTo"))
    for element in list(root.iter(*drop_tags)):
        if in_properties(element) or element.getparent() is None:
            continue
        counts["deletions" if accept else "insertions"] += 1
        element.getparent().remove(element)
    for element in list(root.iter(*keep_tags)):
        if in_properties(element) or element.getparent() is None:
            continue
        counts["insertions" if accept else "deletions"] += 1
        if not accept:
            for t in element.iter(q("delText")):
                t.tag = q("t")
            for t in element.iter(q("delInstrText")):
                t.tag = q("instrText")
        unwrap(element)
    for p in merges:
        following = p.getnext()
        content = [c for c in p if c.tag != q("pPr")]
        has_text = any(c.tag != q("bookmarkStart") and c.tag != q("bookmarkEnd") for c in content)
        if following is not None and following.tag == q("p"):
            at = 1 if len(following) and following[0].tag == q("pPr") else 0
            for offset, child in enumerate(content):
                following.insert(at + offset, child)
            p.getparent().remove(p)
            counts["paragraph joins"] += 1
        elif not has_text:
            p.getparent().remove(p)
            counts["paragraph joins"] += 1
        else:
            print("note: a paragraph mark that is %s has no paragraph after it and stays" % ("deleted" if accept else "inserted"), file=sys.stderr)
    return counts


def cmd_resolve(args, accept):
    pkg = Package(args.docx)
    total = {}
    for name in pkg.text_parts():
        for key, value in resolve(pkg.tree(name), accept).items():
            total[key] = total.get(key, 0) + value
        pkg.mark(name)
    pkg.save(args.output)
    verb = "accepted" if accept else "rejected"
    print("%s: %s" % (verb, ", ".join("%d %s" % (v, k) for k, v in total.items() if v) or "no tracked changes were found"))


# ------------------------------------------------------------------ read

def marked_text(p):
    pieces = []
    for element in p.iter(q("t"), q("delText"), q("tab"), q("br"), q("cr")):
        state = ""
        for ancestor in element.iterancestors():
            if ancestor is p:
                break
            if ancestor.tag in (q("del"), q("moveFrom")):
                state = "del"
                break
            if ancestor.tag in (q("ins"), q("moveTo")):
                state = "ins"
        text = element.text or "" if element.tag in (q("t"), q("delText")) else ("\t" if element.tag == q("tab") else "\n")
        if pieces and pieces[-1][0] == state:
            pieces[-1][1].append(text)
        else:
            pieces.append((state, [text]))
    out = []
    for state, parts in pieces:
        joined = "".join(parts)
        out.append("{+%s+}" % joined if state == "ins" else "[-%s-]" % joined if state == "del" else joined)
    return "".join(out)


def para_line(p, label):
    style = p.find("%s/%s" % (q("pPr"), q("pStyle")))
    notes = []
    if style is not None:
        notes.append(style.get(q("val")))
    if p.find("%s/%s" % (q("pPr"), q("numPr"))) is not None:
        notes.append("list")
    mark = p.find("%s/%s" % (q("pPr"), q("rPr")))
    if mark is not None and mark.find(q("ins")) is not None:
        notes.append("paragraph mark inserted")
    if mark is not None and mark.find(q("del")) is not None:
        notes.append("paragraph mark deleted")
    extras = []
    if p.find(".//" + q("drawing")) is not None or p.find(".//" + q("pict")) is not None:
        extras.append("picture")
    if p.find(".//" + q("fldChar")) is not None or p.find(".//" + q("fldSimple")) is not None:
        extras.append("field")
    return "%s%s%s %s" % (label, " (%s)" % ", ".join(notes) if notes else "", " [%s]" % ", ".join(extras) if extras else "", marked_text(p))


def read_body(body, out):
    paragraph, table = 0, 0
    for child in body:
        if child.tag == q("p"):
            paragraph += 1
            out.append(para_line(child, "p%d" % paragraph))
        elif child.tag == q("tbl"):
            table += 1
            out.append("table %d:" % table)
            for r, row in enumerate(child.findall(q("tr")), start=1):
                cells = []
                for cell in row.findall(q("tc")):
                    cells.append(" / ".join(marked_text(p) for p in cell.iter(q("p"))))
                out.append("  r%d: %s" % (r, " | ".join(cells)))
        elif child.tag == q("sdt"):
            content = child.find(q("sdtContent"))
            if content is not None:
                read_body(content, out)


def cmd_read(args):
    pkg = Package(args.docx)
    out = []
    root = pkg.tree("word/document.xml")
    body = root.find(q("body"))
    out.append("file: %s" % args.docx)
    read_body(body, out)
    sections = list(root.iter(q("sectPr")))
    for sect in sections[-1:]:
        size = sect.find(q("pgSz"))
        if size is not None:
            out.append("page: %s x %s twips, orientation %s" % (size.get(q("w")), size.get(q("h")), size.get(q("orient"), "portrait")))
    for name in pkg.text_parts():
        if name == "word/document.xml":
            continue
        lines = [marked_text(p) for p in pkg.tree(name).iter(q("p"))]
        lines = [x for x in lines if x.strip()]
        if lines:
            out.append("%s: %s" % (name, " | ".join(lines)))
    comments = {}
    if "word/comments.xml" in pkg.data:
        for c in pkg.tree("word/comments.xml").iter(q("comment")):
            comments[c.get(q("id"))] = (c.get(q("author")), c.get(q("date")), " ".join(marked_text(p) for p in c.iter(q("p"))))
    anchors, active = {}, {}
    for element in root.iter(q("commentRangeStart"), q("commentRangeEnd"), q("t"), q("delText")):
        if element.tag == q("commentRangeStart"):
            active[element.get(q("id"))] = []
        elif element.tag == q("commentRangeEnd"):
            anchors[element.get(q("id"))] = "".join(active.pop(element.get(q("id")), []))
        else:
            for buffer in active.values():
                buffer.append(element.text or "")
    for ident, (author, date, text) in comments.items():
        out.append("comment %s by %s (%s) on %r: %s" % (ident, author, date, anchors.get(ident, ""), text))
    changes = {}
    for name in pkg.text_parts():
        for element in pkg.tree(name).iter(q("ins"), q("del"), q("moveFrom"), q("moveTo")):
            key = (etree.QName(element).localname, element.get(q("author")))
            changes[key] = changes.get(key, 0) + 1
    for (kind, author), count in sorted(changes.items()):
        out.append("tracked %s by %s: %d" % (kind, author, count))
    out.append("tracked changes: %d, comments: %d" % (sum(changes.values()), len(comments)))
    print("\n".join(out))


# ------------------------------------------------------------------ check

def view_texts(root, reject_view):
    """Paragraph texts as the original reads (reject_view) or as accepted; empty paragraphs are left out."""
    skip = (q("ins"), q("moveTo")) if reject_view else (q("del"), q("moveFrom"))
    texts = []
    for p in root.iter(q("p")):
        parts = []
        for element in p.iter(q("t"), q("delText"), q("tab"), q("br")):
            if any(a.tag in skip for a in element.iterancestors()):
                continue
            parts.append(element.text or "" if element.tag in (q("t"), q("delText")) else " ")
        text = "".join(parts)
        if text.strip():
            texts.append(text)
    return texts


def cmd_check(args):
    problems = []
    pkg = Package(args.docx)
    try:
        with zipfile.ZipFile(args.docx) as archive:
            bad = archive.testzip()
            if bad:
                problems.append("the ZIP entry %s is damaged" % bad)
            folders = [i.filename for i in archive.infolist() if i.is_dir()]
            if folders:
                problems.append("the ZIP holds folder entries (%s); some services refuse such files; write the file again with the pack subcommand" % ", ".join(folders[:3]))
    except zipfile.BadZipFile as exc:
        problems.append(str(exc))
    for required in ("[Content_Types].xml", "_rels/.rels"):
        if required not in pkg.data:
            problems.append("the package has no %s" % required)
    for name in pkg.order:
        if name.endswith((".xml", ".rels")):
            parser = etree.XMLParser(resolve_entities=False, no_network=True, load_dtd=False)
            try:
                etree.fromstring(pkg.data[name], parser)
            except etree.XMLSyntaxError as exc:
                problems.append("%s is not well-formed: %s" % (name, exc))
    if not problems:
        defaults, overrides = set(), set()
        for element in pkg.tree("[Content_Types].xml"):
            if element.tag == "{%s}Default" % CT_NS:
                defaults.add(element.get("Extension", "").lower())
            elif element.tag == "{%s}Override" % CT_NS:
                overrides.add(element.get("PartName", ""))
        for name in pkg.order:
            if name == "[Content_Types].xml":
                continue
            if "/" + name not in overrides and name.rsplit(".", 1)[-1].lower() not in defaults:
                problems.append("%s has no content type" % name)
        for name in [n for n in pkg.order if n.endswith(".rels")]:
            base = os.path.dirname(os.path.dirname(name))
            for rel in pkg.tree(name):
                if rel.get("TargetMode") == "External":
                    continue
                target = rel.get("Target", "")
                full = target.lstrip("/") if target.startswith("/") else os.path.normpath(os.path.join(base, target)).replace("\\", "/")
                if full not in pkg.data:
                    problems.append("%s points to the missing part %s" % (name, full))
        seen = {}
        for name in pkg.text_parts():
            root = pkg.tree(name)
            for element in root.iter(q("ins"), q("del")):
                if element.getparent().tag == q("rPr") or element.getparent().tag == q("trPr"):
                    continue
                ident = element.get(q("id"))
                if ident in seen:
                    problems.append("tracked change id %s is used twice (%s)" % (ident, name))
                seen[ident] = name
                if element.get(q("author")) is None:
                    problems.append("a tracked change in %s has no author" % name)
            for element in root.iter(q("del")):
                if element.getparent().tag in (q("rPr"), q("trPr")):
                    continue
                if next(element.iter(q("t")), None) is not None:
                    problems.append("a tracked deletion in %s holds text in w:t; deleted text belongs in w:delText" % name)
            for element in root.iter(q("ins")):
                if element.getparent().tag in (q("rPr"), q("trPr")):
                    continue
                if next(element.iter(q("delText")), None) is not None:
                    problems.append("a tracked insertion in %s holds w:delText" % name)
        document = pkg.tree("word/document.xml")
        have = {c.get(q("id")) for c in pkg.tree("word/comments.xml").iter(q("comment"))} if "word/comments.xml" in pkg.data else set()
        starts = {e.get(q("id")) for e in document.iter(q("commentRangeStart"))}
        ends = {e.get(q("id")) for e in document.iter(q("commentRangeEnd"))}
        refs = {e.get(q("id")) for e in document.iter(q("commentReference"))}
        for ident in sorted(starts | ends | refs):
            if ident not in have:
                problems.append("comment %s is anchored in the text but has no comment entry" % ident)
            if not (ident in starts and ident in ends and ident in refs):
                problems.append("comment %s lacks its start, end or reference marker" % ident)
        for ident in sorted(have - starts):
            problems.append("comment %s has no anchor in the text" % ident)
    if args.original and not problems:
        before = Package(args.original)
        for name in pkg.text_parts():
            if name not in before.data:
                continue
            mine = view_texts(pkg.tree(name), True)
            theirs = view_texts(before.tree(name), True)
            if mine != theirs:
                for a, b in zip(mine, theirs):
                    if a != b:
                        problems.append("text changed without tracking in %s: original %r, now %r" % (name, b[:80], a[:80]))
                        break
                else:
                    problems.append("paragraphs were added or removed without tracking in %s (%d now, %d originally)" % (name, len(mine), len(theirs)))
    try:
        import docx
        document_obj = docx.Document(args.docx)
        print("python-docx opens it: %d paragraphs, %d tables" % (len(document_obj.paragraphs), len(document_obj.tables)))
    except ImportError:
        print("note: python-docx is not installed; that check was skipped")
    except Exception as exc:
        problems.append("python-docx cannot open the file: %s" % exc)
    if problems:
        for line in problems:
            print("FAIL: %s" % line, file=sys.stderr)
        sys.exit(1)
    print("all checks passed")


# ------------------------------------------------------------------ unpack and pack

def cmd_unpack(args):
    pkg = Package(args.docx)
    if os.path.exists(args.folder) and os.listdir(args.folder):
        fail("%s exists and is not empty" % args.folder)
    for name in pkg.order:
        target = os.path.join(args.folder, name)
        os.makedirs(os.path.dirname(target), exist_ok=True)
        with open(target, "wb") as handle:
            handle.write(pkg.data[name])
    print("unpacked %d parts into %s; edit word/document.xml without reformatting it, then use the pack subcommand" % (len(pkg.order), args.folder))


def cmd_pack(args):
    if not os.path.isdir(args.folder):
        fail("%s is not a folder" % args.folder)
    if os.path.exists(args.output):
        fail("%s exists; choose a new file name" % args.output)
    files = []
    for base, _, names in os.walk(args.folder):
        for name in names:
            full = os.path.join(base, name)
            files.append((os.path.relpath(full, args.folder).replace(os.sep, "/"), full))
    names = sorted(files)
    names.sort(key=lambda item: item[0] != "[Content_Types].xml")
    if not names or names[0][0] != "[Content_Types].xml":
        fail("the folder has no [Content_Types].xml")
    for rel, full in names:
        if rel.endswith((".xml", ".rels")):
            try:
                etree.parse(full, etree.XMLParser(resolve_entities=False, no_network=True, load_dtd=False))
            except etree.XMLSyntaxError as exc:
                fail("%s is not well-formed XML: %s" % (rel, exc))
    with zipfile.ZipFile(args.output, "w", zipfile.ZIP_DEFLATED) as archive:
        for rel, full in names:
            archive.write(full, rel)
    print("wrote %s (%d parts)" % (args.output, len(names)))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)

    def match_options(p):
        p.add_argument("--nth", type=int, help="act on the Nth match only (1-based)")
        p.add_argument("--all", action="store_true", help="act on every match")

    p = sub.add_parser("read", help="print the structure and text")
    p.add_argument("docx")
    p.set_defaults(func=cmd_read)

    p = sub.add_parser("replace", help="change text, tracked or in place")
    p.add_argument("docx")
    p.add_argument("-o", "--output", required=True)
    p.add_argument("--find")
    p.add_argument("--replace")
    p.add_argument("--map", help="JSON object {find: replacement} or list of {find, replace}")
    p.add_argument("--tracked", action="store_true", help="record the change as a tracked change")
    p.add_argument("--whole", action="store_true", help="with --tracked, mark the whole found text instead of only the words that differ")
    p.add_argument("--author", default="Assistant", help="author name of tracked changes")
    p.add_argument("--body-only", action="store_true", help="leave headers, footers and notes alone")
    match_options(p)
    p.set_defaults(func=cmd_replace)

    p = sub.add_parser("comment", help="add a comment anchored to text")
    p.add_argument("docx")
    p.add_argument("-o", "--output", required=True)
    p.add_argument("--find", required=True, help="the text the comment is about")
    p.add_argument("--text", required=True, help="the comment")
    p.add_argument("--author", default="Assistant")
    p.add_argument("--initials")
    match_options(p)
    p.set_defaults(func=cmd_comment)

    for name, accept in (("accept", True), ("reject", False)):
        p = sub.add_parser(name, help="%s every tracked change" % name)
        p.add_argument("docx")
        p.add_argument("-o", "--output", required=True)
        p.set_defaults(func=(lambda a, accept=accept: cmd_resolve(a, accept)))

    p = sub.add_parser("check", help="test the package and, with --original, that all text changes are tracked")
    p.add_argument("docx")
    p.add_argument("--original", help="the file the edit started from")
    p.set_defaults(func=cmd_check)

    p = sub.add_parser("unpack", help="extract the package to a new folder")
    p.add_argument("docx")
    p.add_argument("folder")
    p.set_defaults(func=cmd_unpack)

    p = sub.add_parser("pack", help="zip a folder into a new .docx")
    p.add_argument("folder")
    p.add_argument("-o", "--output", required=True)
    p.set_defaults(func=cmd_pack)

    args = parser.parse_args(argv)
    if etree is None:
        fail("the lxml library is not installed", 2)
    args.func(args)
    return 0


if __name__ == "__main__":
    sys.exit(main())
