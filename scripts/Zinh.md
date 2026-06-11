---
script: Zinh
abbr_short: zi
name: Code for inherited script
requires_font: false
unicode: false
translations:
  - translation_iso: ara
    name: موروث
    auto: true
  - translation_iso: ben
    name: উত্তরাধিকারসূত্রে প্রাপ্ত লিপির জন্য কোড
    auto: true
  - translation_iso: deu
    name: Code für geerbte Schrift
    auto: true
  - translation_iso: fra
    name: écriture héritée
  - translation_iso: hin
    name: विरासती लिपि के लिए कोड
    auto: true
  - translation_iso: ind
    name: kode untuk aksara warisan
    auto: true
  - translation_iso: jpn
    name: 継承された文字体系
    auto: true
  - translation_iso: kor
    name: 상속 문자
    auto: true
  - translation_iso: por
    name: Código para escrita herdada
    auto: true
  - translation_iso: rus
    name: код для унаследованной письменности
    auto: true
  - translation_iso: spa
    name: código para escritura heredada
    auto: true
  - translation_iso: swa
    name: msimbo wa hati iliyorithiwa
    auto: true
  - translation_iso: urd
    name: موروثی رسم الخط کا کوڈ
    auto: true
  - translation_iso: zho
    name: 继承文字
    auto: true
---

The ISO 15924 code *Zinh* (also represented in Unicode as *Inherited*) is not a script in the conventional sense but a classification used to tag characters that inherit the script property of the preceding character in a text. This includes combining diacritical marks and other combining characters that can attach to base characters from many different scripts, as well as characters such as the zero-width joiner and non-joiner. The Inherited classification is used in Unicode's bidirectional algorithm and script-run detection to allow such characters to be treated as belonging to whichever script surrounds them, rather than being assigned a fixed script identity.
