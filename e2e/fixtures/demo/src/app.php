<?php
namespace App;

final class Invoice
{
    public function total(array $lines): float
    {
        return array_sum($lines) * 1.2; // including VAT
    }
}
