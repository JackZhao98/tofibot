#!/usr/bin/env python3
"""Change the slide structure of an existing deck without touching slide content.

  pptx_slides.py deck.pptx layouts                    list layouts and their placeholders
  pptx_slides.py deck.pptx -o out.pptx duplicate N    copy slide N right after itself
  pptx_slides.py deck.pptx -o out.pptx delete N       remove slide N
  pptx_slides.py deck.pptx -o out.pptx move N M       make slide N the Mth slide

Slide numbers start at 1. The output file is required for the three edits and must
differ from the input; the input is never edited. A duplicated slide shares its
chart, picture and embedded-object parts with the source, so changing a shared
chart changes both slides. Deleting a slide drops its parts from the saved file.
Do the structural work first and edit slide content afterwards, because a
duplicate copies whatever the source holds at that moment.
Exit status: 0 done; 2 unreadable deck, bad number or bad arguments.
"""
from __future__ import annotations

import argparse
import copy
import os
import sys

R_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
SKIP_RELS = ("slideLayout", "notesSlide")


def duplicate(prs, number):
    source = prs.slides[number - 1]
    new = prs.slides.add_slide(source.slide_layout)
    tree = new.shapes._spTree
    for element in list(tree):
        if element.tag.endswith("}sp") or element.tag.endswith("}pic") or element.tag.endswith("}graphicFrame") \
                or element.tag.endswith("}grpSp") or element.tag.endswith("}cxnSp"):
            tree.remove(element)
    mapping = {}
    for rid, rel in source.part.rels.items():
        if rel.reltype.endswith(SKIP_RELS):
            continue
        if rel.is_external:
            mapping[rid] = new.part.relate_to(rel.target_ref, rel.reltype, is_external=True)
        else:
            mapping[rid] = new.part.relate_to(rel.target_part, rel.reltype)
    for element in source.shapes._spTree:
        if element.tag.endswith(("}nvGrpSpPr", "}grpSpPr", "}extLst")):
            continue
        clone = copy.deepcopy(element)
        for node in clone.iter():
            for attribute in ("embed", "link", "id", "pict"):
                key = "{%s}%s" % (R_NS, attribute)
                if key in node.attrib and node.attrib[key] in mapping:
                    node.attrib[key] = mapping[node.attrib[key]]
        tree.append(clone)
    background = source._element.cSld.find("{http://schemas.openxmlformats.org/presentationml/2006/main}bg")
    if background is not None:
        new._element.cSld.insert(0, copy.deepcopy(background))
    if source.has_notes_slide:
        new.notes_slide.notes_text_frame.text = source.notes_slide.notes_text_frame.text
    move(prs, len(prs.slides), number + 1)
    return number + 1


def delete(prs, number):
    ids = prs.slides._sldIdLst
    entry = list(ids)[number - 1]
    prs.part.drop_rel(entry.rId)
    ids.remove(entry)


def move(prs, number, target):
    ids = prs.slides._sldIdLst
    entry = list(ids)[number - 1]
    ids.remove(entry)
    ids.insert(target - 1, entry)


def layouts(prs):
    lines = []
    for index, layout in enumerate(prs.slide_layouts):
        holders = ["%s:%s" % (p.placeholder_format.idx, str(p.placeholder_format.type).split(".")[-1].split(" ")[0])
                   for p in layout.placeholders]
        lines.append("layout %d %r placeholders: %s" % (index, layout.name, ", ".join(holders) or "none"))
    return lines


def check_number(prs, number, label):
    if not 1 <= number <= len(prs.slides):
        raise ValueError("%s %d is outside 1..%d" % (label, number, len(prs.slides)))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("deck")
    parser.add_argument("-o", "--output")
    parser.add_argument("action", choices=["layouts", "duplicate", "delete", "move"])
    parser.add_argument("numbers", nargs="*", type=int)
    args = parser.parse_args(argv)
    try:
        from pptx import Presentation
        prs = Presentation(args.deck)
        if args.action == "layouts":
            print("\n".join(layouts(prs)))
            return 0
        if not args.output or os.path.abspath(args.output) == os.path.abspath(args.deck):
            raise ValueError("give an output path with -o that differs from the input")
        want = 2 if args.action == "move" else 1
        if len(args.numbers) != want:
            raise ValueError("%s takes %d slide number(s)" % (args.action, want))
        for number in args.numbers:
            check_number(prs, number, "slide")
        if args.action == "duplicate":
            print("new slide is number %d" % duplicate(prs, args.numbers[0]))
        elif args.action == "delete":
            delete(prs, args.numbers[0])
        else:
            move(prs, *args.numbers)
        prs.save(args.output)
        print("wrote %s (%d slides)" % (args.output, len(prs.slides)))
        return 0
    except (OSError, ValueError, KeyError, ImportError) as exc:
        print("cannot do that: %s" % exc, file=sys.stderr)
        return 2
    except Exception as exc:  # python-pptx raises several package errors for a damaged file
        print("cannot read the deck: %s" % exc, file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
