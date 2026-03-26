"""Order processing system."""

from typing import List, Dict


class OrderProcessor:
    def __init__(self):
        self.tax_rate = 0.08  # 8% tax
    
    def calculate_subtotal(self, items: List[Dict]) -> float:
        """Calculate subtotal for items."""
        return sum(item["price"] * item["quantity"] for item in items)
    
    def apply_discount(self, subtotal: float, discount_percent: float) -> float:
        """Apply discount to subtotal.
        
        BUG: This function applies discount before tax, but should apply after.
        However, the REAL bug is in calculate_discount_amount which rounds incorrectly.
        """
        discount_amount = self.calculate_discount_amount(subtotal, discount_percent)
        return subtotal - discount_amount
    
    def calculate_discount_amount(self, subtotal: float, discount_percent: float) -> float:
        """Calculate discount amount.
        
        BUG: Rounds to nearest dollar instead of cent, causing $10 discount to become $5.
        """
        raw_discount = subtotal * (discount_percent / 100)
        # BUG: Should be round(raw_discount, 2) for cents
        return round(raw_discount, 0)  # Rounds to nearest dollar
    
    def calculate_tax(self, amount: float) -> float:
        """Calculate tax on amount."""
        return amount * self.tax_rate
    
    def process_order(self, items: List[Dict], discount_percent: float = 0) -> Dict:
        """Process an order and return final total."""
        subtotal = self.calculate_subtotal(items)
        
        discounted_subtotal = self.apply_discount(subtotal, discount_percent)
        
        tax = self.calculate_tax(discounted_subtotal)
        
        total = discounted_subtotal + tax
        
        return {
            "subtotal": subtotal,
            "discount_percent": discount_percent,
            "discounted_subtotal": discounted_subtotal,
            "tax": tax,
            "total": total
        }